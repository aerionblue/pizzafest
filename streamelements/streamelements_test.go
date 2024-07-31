package streamelements

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/aerionblue/pizzafest/donation"
)

const (
	donationJson1 = `{"_id":"d1","type":"tip","provider":"twitch","channel":"testing","createdAt":"2024-07-31T08:07:10.524Z","data": {"amount":12.34,"currency":"USD","username":"test1","tipId":"abc1","message":"team mid","avatar":"d1.png"},"updatedAt":"2024-07-31T08:07:10.524Z"}`
	donationJson2 = `{"_id":"d2","type":"tip","provider":"twitch","channel":"testing","createdAt":"2024-07-31T08:07:12.524Z","data": {"amount":100,"currency":"USD","username":"test2","tipId":"abc2","message":"team left","avatar":"d2.png"},"updatedAt":"2024-07-31T08:07:12.524Z"}`
	timeStr1      = "2024-07-31T08:07:10.524Z"
	timeStr2      = "2024-07-31T08:07:12.524Z"
)

func TestParseDonationResponse(t *testing.T) {
	time1, err := time.Parse(time.RFC3339, timeStr1)
	if err != nil {
		t.Fatal(err)
	}
	time2, err := time.Parse(time.RFC3339, timeStr2)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		jsonResp  string
		wantTimes []time.Time
		wantEvs   []donation.Event
	}{
		{
			"zero donations",
			`[]`,
			nil,
			nil,
		},
		{
			"one donation",
			makeJsonResp(donationJson1),
			[]time.Time{time1},
			[]donation.Event{{Owner: "test1", Channel: "testing", Cash: donation.CentsValue(1234), Message: "team mid"}},
		},
		{
			"two donations",
			makeJsonResp(donationJson2, donationJson1),
			[]time.Time{time1, time2},
			[]donation.Event{
				{Owner: "test1", Channel: "testing", Cash: donation.CentsValue(1234), Message: "team mid"},
				{Owner: "test2", Channel: "testing", Cash: donation.CentsValue(10000), Message: "team left"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Log(tc.jsonResp)
			evs, times, err := parseDonationResponse([]byte(tc.jsonResp), "testing")
			if err != nil {
				t.Errorf("error parsing json: %v", err)
			}
			if !cmp.Equal(evs, tc.wantEvs) {
				t.Errorf(cmp.Diff(evs, tc.wantEvs))
			}
			if !cmp.Equal(times, tc.wantTimes) {
				t.Errorf("wrong last donation ID: got %v, want %v", times, tc.wantTimes)
			}
		})
	}
}

func makeJsonResp(donations ...string) string {
	return fmt.Sprintf(`[%s]`, strings.Join(donations, ","))
}
