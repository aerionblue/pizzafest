package streamlabs

import (
	"encoding/json"
	"time"
)

// donationResponse is the response to the GET /donations request.
type donationResponse struct {
	Donations []donationData `json:"data"`
}

type donationData struct {
	DonationID int          `json:"donation_id"`
	CreatedAt  donationTime `json:"created_at"`    // Seconds since the epoch.
	Dollars    float64      `json:"amount,string"` // The decimal dollar amount.
	Donator    string       `json:"name"`
	Message    string
}

type donationTime time.Time

func (t *donationTime) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*t = donationTime(time.Unix(int64(f), 0))
	return nil
}
