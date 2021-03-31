// Package streamlabs reads donation info from the Streamlabs API.
package streamlabs

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
	"strconv"
	"time"

	"github.com/aerionblue/pizzafest/donation"
)

const pollInterval = 30 * time.Second
const donationBaseUrl = "https://streamlabs.com/api/v1.0/donations"

type DonationPoller struct {
	ticker *time.Ticker
	stop   chan interface{}

	accessToken      string
	lastDonationID   int
	donationCallback func(donation.Event)
}

// NewDonationPoller creates a DonationPoller that calls the provided callback once for each donation.
func NewDonationPoller(ctx context.Context, credsPath string) (*DonationPoller, error) {
	accessToken, err := parseCreds(credsPath)
	if err != nil {
		return nil, err
	}
	d := &DonationPoller{
		ticker:      time.NewTicker(pollInterval),
		stop:        make(chan interface{}),
		accessToken: accessToken,
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
	_, lastID, err := d.doDonationRequest(1, 0)
	if err != nil {
		return err
	}
	d.lastDonationID = lastID
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
	evs, lastID, err := d.doDonationRequest(10, d.lastDonationID)
	if err != nil {
		log.Printf("donation poll failed: %v", err)
		return
	}
	d.lastDonationID = lastID
	for _, ev := range evs {
		d.donationCallback(ev)
	}
}

// doDonationRequest fetches donations from Streamlabs. It returns the parsed
// donations in chronological order, and the ID of the most recent donation.
func (d DonationPoller) doDonationRequest(limit int, lastID int) ([]donation.Event, int, error) {
	u, err := url.Parse(donationBaseUrl)
	if err != nil {
		panic(err)
	}
	q := u.Query()
	q.Set("access_token", d.accessToken)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("currency", "USD")
	if lastID != 0 {
		q.Set("after", strconv.Itoa(lastID))
	}
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, 0, fmt.Errorf("error polling Streamlabs: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("error reading Streamlabs response: %v", err)
	}
	evs, ids, err := parseDonationResponse(raw)
	if err != nil {
		return nil, 0, err
	}
	if len(evs) == 0 {
		return nil, lastID, nil
	}
	return evs, ids[len(ids)-1], nil
}

// parseDonationResponse parses the JSON response, returning a list of events
// in chronological order and a corresponding list of donation IDs.
func parseDonationResponse(raw []byte) ([]donation.Event, []int, error) {
	var dr donationResponse
	err := json.Unmarshal(raw, &dr)
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing Streamlabs response: %v", err)
	}
	if len(dr.Donations) == 0 {
		return nil, nil, nil
	}
	// The API promises the response is sorted in reverse chronological order.
	var evs []donation.Event
	var ids []int
	for i := len(dr.Donations) - 1; i >= 0; i = i - 1 {
		d := dr.Donations[i]
		evs = append(evs, donation.Event{
			Owner:   d.Donator,
			Cents:   int(d.Dollars * 100),
			Message: d.Message,
		})
		ids = append(ids, d.DonationID)
	}
	return evs, ids, nil
}

type tokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func parseCreds(path string) (string, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("couldn't read Streamlabs credentials file: %v", err)
	}
	var t tokens
	if err := json.Unmarshal(data, &t); err != nil {
		return "", fmt.Errorf("couldn't parse Streamlabs credentials: %v", err)
	}
	if t.AccessToken == "" {
		return "", errors.New("access token missing from Streamlabs credentials file")
	}
	return t.AccessToken, nil
}
