package streamlabs

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/aerionblue/pizzafest/donation"
)

const donationJson1 = `{"amount": "11.0000000000","created_at": 1616710000,"currency": "USD","donation_id": 1000,"message": "team mid","name": "ShartyMcFly"}`
const donationJson2 = `{"amount": "100.0000000000","created_at": 1616720000,"currency": "USD","donation_id": 2000,"message": "team left","name": "Konagami"}`

func TestParseDonationResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		jsonResp string
		wantID   int
		want     []donation.Event
	}{
		{
			"zero donations",
			`{"data": []}`,
			0,
			nil,
		},
		{
			"one donation",
			makeJsonResp(donationJson1),
			1000,
			[]donation.Event{{Owner: "ShartyMcFly", Cents: 1100, Message: "team mid"}},
		},
		{
			"two donations",
			makeJsonResp(donationJson2, donationJson1),
			2000,
			[]donation.Event{
				{Owner: "ShartyMcFly", Cents: 1100, Message: "team mid"},
				{Owner: "Konagami", Cents: 10000, Message: "team left"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, lastID, err := parseDonationResponse([]byte(tc.jsonResp))
			if err != nil {
				t.Errorf("error parsing json: %v", err)
			}
			if !cmp.Equal(got, tc.want) {
				t.Errorf(cmp.Diff(got, tc.want))
			}
			if lastID != tc.wantID {
				t.Errorf("wrong last donation ID: got %v, want %v", lastID, tc.wantID)
			}
		})
	}
}

func makeJsonResp(donations ...string) string {
	return fmt.Sprintf(`{"data": [%s]}`, strings.Join(donations, ","))
}
