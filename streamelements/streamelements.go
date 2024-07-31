// Package streamelements reads donation info from the StreamElements API.
package streamelements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/aerionblue/pizzafest/donation"
)

const pollInterval = 30 * time.Second

const (
	activityFeedUrlTemplate = "https://api.streamelements.com/kappa/v2/activities/%s"
	userInfoBaseUrl         = "https://api.streamelements.com/kappa/v2/users/current"

	acceptContentType = "application/json; charset=utf-8"
	apiOrigin         = "pizzafestbot"
)

var /* const */ channelIDPattern = regexp.MustCompile("^[0-9a-f]+$")

// TODO(aerion): Factor out the polling logic from here and the streamlabs package.

type DonationPoller struct {
	// The Twitch channel towards which these donations are being made.
	twitchChannel string
	// The ID of the StreamElements channel. A 24-character hex string.
	seChannelID string
	ticker      *time.Ticker
	stop        chan interface{}

	// The JWT token for the StreamElements account.
	authToken string
	// The creation time of the last donation that was read.
	lastDonationTime time.Time
	donationCallback func(donation.Event)
}

// NewDonationPoller creates a DonationPoller that calls the provided callback once for each donation.
func NewDonationPoller(ctx context.Context, credsPath string, twitchChannel string) (*DonationPoller, error) {
	creds, err := parseCreds(credsPath)
	if err != nil {
		return nil, err
	}
	d := &DonationPoller{
		// We could query StreamElements for the Twitch channel associated with the
		// account, but it's not necessarily the same as the channel we are
		// operating in (especially when testing).
		twitchChannel: twitchChannel,
		seChannelID:   creds.ChannelID,
		ticker:        time.NewTicker(pollInterval),
		stop:          make(chan interface{}),
		authToken:     creds.AuthToken,
	}
	return d, nil
}

func (d *DonationPoller) OnDonation(cb func(donation.Event)) {
	d.donationCallback = cb
}

// Start starts polling for donations.
func (d *DonationPoller) Start() error {
	if d.donationCallback == nil {
		panic("non-nil donation callback must be provided to OnDonation before calling Start")
	}
	username, err := d.doUserRequest()
	if err != nil {
		return err
	} else if username == "" {
		return errors.New("could not find StreamElements username")
	}
	log.Printf("starting StreamElements polling for %s", username)
	// Fetch 1 donation. This assumes that the StreamElements API returns the
	// newest events first. The documentation doesn't actually say that it does
	// this, but honestly, it doesn't say a lot of things.
	evs, lastTime, err := d.doDonationRequest(1)
	if err != nil {
		return err
	}
	d.lastDonationTime = lastTime
	if len(evs) != 0 {
		log.Printf("the last known donation is for $%s from %s", evs[0].Value(), evs[0].Owner)
	}
	go func() {
		for {
			select {
			case <-d.stop:
				return
			case <-d.ticker.C:
				d.poll()
			}
		}
	}()
	return nil
}

// Stop stops polling.
func (d *DonationPoller) Stop() {
	if d.stop != nil {
		close(d.stop)
	}
	if d.ticker != nil {
		d.ticker.Stop()
	}
}

func (d *DonationPoller) poll() {
	evs, lastTime, err := d.doDonationRequest(10)
	if err != nil {
		log.Printf("donation poll failed: %v", err)
		return
	}
	d.lastDonationTime = lastTime
	for _, ev := range evs {
		d.donationCallback(ev)
	}
}

func (d *DonationPoller) createAPIRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error initializing StreamElements request: %v", err)
	}
	req.Header.Add("Accept", acceptContentType)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", d.authToken))
	return req, nil
}

func (d *DonationPoller) getActivityFeedUrl() (*url.URL, error) {
	return url.Parse(fmt.Sprintf(activityFeedUrlTemplate, d.seChannelID))
}

// doUserRequest fetches the username of the StreamElements account.
func (d *DonationPoller) doUserRequest() (string, error) {
	req, err := d.createAPIRequest(userInfoBaseUrl)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error fetching StreamElements user info: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading StreamElements response: %v", err)
	}
	username, err := parseUserResponse(raw)
	if err != nil {
		return "", fmt.Errorf("error parsing StreamElements response: %v", err)
	}
	return username, nil
}

// doDonationRequest fetches donations from StreamElements. It returns the parsed
// donations in chronological order, and the time of the most recent donation.
func (d *DonationPoller) doDonationRequest(limit int) ([]donation.Event, time.Time, error) {
	u, err := d.getActivityFeedUrl()
	if err != nil {
		return nil, time.Time{}, err
	}
	q := u.Query()
	q.Set("origin", apiOrigin)
	q.Set("limit", strconv.Itoa(limit))
	// TODO(aerion): Adding +1s here should be fine, but theoretically we
	// could miss an event. Consider just tracking all the IDs we've seen so
	// far during this session.
	q.Set("after", d.lastDonationTime.Add(1*time.Second).Format(time.RFC3339))
	q.Set("before", time.Now().Format(time.RFC3339))
	// All these bounds are required parameters even if you're only asking for tips.
	q.Set("mincheer", "0")
	q.Set("minhost", "0")
	q.Set("minsub", "0")
	q.Set("mintop", "0")
	q.Set("types", "tip")
	u.RawQuery = q.Encode()
	req, err := d.createAPIRequest(u.String())
	if err != nil {
		return nil, time.Time{}, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("error polling StreamElements: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("error reading StreamElements response: %v", err)
	}
	evs, times, err := parseDonationResponse(raw, d.twitchChannel)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("error parsing StreamElements response: %v", err)
	}
	if len(evs) == 0 {
		return nil, d.lastDonationTime, nil
	}
	return evs, times[len(times)-1], nil
}

type userResponse struct {
	Username string
}

func parseUserResponse(raw []byte) (string, error) {
	var ur userResponse
	err := json.Unmarshal(raw, &ur)
	if err != nil {
		return "", err
	}
	return ur.Username, nil
}

// parseDonationResponse parses the JSON response, returning a list of events
// in chronological order and a corresponding list of times at which the donations were made.
func parseDonationResponse(raw []byte, twitchChannel string) ([]donation.Event, []time.Time, error) {
	// TODO(aerion): Give this function a DonationPoller receiver instead of
	// passing the Twitch channel by argument.
	var activities []seActivity
	err := json.Unmarshal(raw, &activities)
	if err != nil {
		return nil, nil, err
	}
	if len(activities) == 0 {
		return nil, nil, nil
	}
	sort.Sort(byCreationTime(activities))
	var evs []donation.Event
	var times []time.Time
	for i := 0; i < len(activities); i++ {
		a := activities[i]
		if a.Data.Currency != "USD" {
			log.Printf("ignoring Unamerican donation of %.2f %s", a.Data.Dollars, a.Data.Currency)
			continue
		}
		evs = append(evs, donation.Event{
			Owner:   a.Data.Donator,
			Channel: twitchChannel,
			Cash:    donation.CentsValue(int(a.Data.Dollars * 100)),
			Message: a.Data.Message,
		})
		times = append(times, a.Time())
	}
	return evs, times, nil
}

type seCreds struct {
	ChannelID string `json:"channelId"`
	AuthToken string `json:"jwtToken"`
}

func parseCreds(path string) (seCreds, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return seCreds{}, fmt.Errorf("couldn't read StreamElements credentials file: %v", err)
	}
	var creds seCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return seCreds{}, fmt.Errorf("couldn't parse StreamElements credentials: %v", err)
	}
	if !channelIDPattern.MatchString(creds.ChannelID) {
		return seCreds{}, fmt.Errorf("channel ID in StreamElements credentials file must match %s", channelIDPattern)
	}
	if creds.AuthToken == "" {
		return seCreds{}, errors.New("auth token missing from StreamElements credentials file")
	}
	return creds, nil
}
