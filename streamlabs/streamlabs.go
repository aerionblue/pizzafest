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

func NewDonationPoller(ctx context.Context, credsPath string) (*DonationPoller, error) {
	accessToken, err := parseCreds(credsPath)
	if err != nil {
		return nil, err
	}
	d := &DonationPoller{
		ticker:      time.NewTicker(pollInterval),
		stop:        make(chan interface{}),
		accessToken: accessToken,
		// TODO(aerion): Initialize lastDonationID to the most recent donation
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
	return d, nil
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

func (d *DonationPoller) OnDonation(cb func(donation.Event)) {
	d.donationCallback = cb
}

func (d *DonationPoller) poll() {
	u, err := url.Parse(donationBaseUrl)
	if err != nil {
		panic(err)
	}
	q := u.Query()
	q.Set("access_token", d.accessToken)
	q.Set("limit", "10")
	q.Set("currency", "USD")
	if d.lastDonationID != 0 {
		q.Set("after", strconv.Itoa(d.lastDonationID))
	}
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		log.Printf("error polling Streamlabs: %v", err)
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading Streamlabs response: %v", err)
		return
	}

	evs, lastID, err := parseDonationResponse(raw)
	if err != nil {
		log.Printf("error parsing Streamlabs response: %v", err)
		return
	}
	if len(evs) == 0 {
		return
	}
	d.lastDonationID = lastID
	if d.donationCallback == nil {
		return
	}
	for _, ev := range evs {
		d.donationCallback(ev)
	}
}

func parseDonationResponse(raw []byte) ([]donation.Event, int, error) {
	var dr donationResponse
	err := json.Unmarshal(raw, &dr)
	if err != nil {
		return nil, 0, err
	}
	donations := dr.Donations
	if len(donations) == 0 {
		return nil, 0, nil
	}

	// The API promises the response is sorted in reverse chronological order.
	// We process donations in forward chronological order.
	lastID := donations[0].DonationID
	var evs []donation.Event
	for i := len(donations) - 1; i >= 0; i = i - 1 {
		d := donations[i]
		evs = append(evs, donation.Event{
			Owner:   d.Donator,
			Cents:   int(d.Dollars * 100),
			Message: d.Message,
		})
	}
	return evs, lastID, nil
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
