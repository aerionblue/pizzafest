package streamelements

import (
	"encoding/json"
	"time"
)

// donationData describes a member of the activity feed response. The response
// to the GET /activities/:channel request is a list of these objects.
type seActivity struct {
	DonationID string       `json:"_id"`
	CreatedAt  donationTime `json:"createdAt"` // ISO 8601 date
	Data       donationData `json:"data"`
}

type donationData struct {
	Dollars  float64 `json:"amount"` // The decimal dollar amount.
	Currency string  `json:"currency"`
	Donator  string  `json:"username"`
	Message  string
}

func (d seActivity) Time() time.Time {
	return time.Time(d.CreatedAt)
}

type donationTime time.Time

func (t *donationTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsedTime, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	*t = donationTime(parsedTime)
	return nil
}

type byCreationTime []seActivity

func (t byCreationTime) Len() int           { return len(t) }
func (t byCreationTime) Swap(i, j int)      { t[i], t[j] = t[j], t[i] }
func (t byCreationTime) Less(i, j int) bool { return t[i].Time().Before(t[j].Time()) }
