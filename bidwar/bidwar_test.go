package bidwar

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/aerionblue/pizzafest/donation"
	"github.com/go-test/deep"
	"google.golang.org/api/sheets/v4"
)

const testJSON = `{
    "contests": [
        {
            "name": "Mario Kart track",
            "options": [
                {"displayName": "Moo Moo Meadows", "shortCode": "Moo", "aliases": ["moo", "moomoo"]},
                {"displayName": "Neo Bowser City", "shortCode": "NBC", "aliases": ["neo", "nbc"]}
            ]
        },
        {
            "name": "Featuring Dante From The Devil May Cry Series",
            "options": [
                {"displayName": "Devil May Cry", "shortCode": "DMC1", "aliases": ["dmc", "dmc1"]},
                {"displayName": "Devil May Cry 2", "shortCode": "DMC2", "aliases": ["dmc2"]},
                {"displayName": "Devil May Cry 3", "shortCode": "DMC3", "aliases": ["dmc3"]}
            ]
        }
    ]
}
`

func TestChoiceFromMessage(t *testing.T) {
	bidwars, err := Parse([]byte(testJSON))
	if err != nil {
		t.Fatalf("error parsing test data: %v", err)
	}

	for _, tc := range []struct {
		desc string
		msg  string
		want string // The ShortCode of the wanted Option
	}{
		{"simple match", "put this towards moo moo meadows", "Moo"},
		{"ignore surrounding punctiation", "i said 'moo...'", "Moo"},
		{"returns earliest match", "nbc, no i meant moo moo", "NBC"},
		{"matches are case-insensitive", "MoO!", "Moo"},
		{"numbers are part of the word", "i pick dmc2", "DMC2"},
		{"must match whole word", "go to dmc", "DMC1"},
		{"substrings don't count", "dmca takedown", ""},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got := bidwars.ChoiceFromMessage(tc.msg, FromChatMessage)
			if got.Option.ShortCode != tc.want {
				t.Errorf("got %q, want %q", got.Option.ShortCode, tc.want)
			}
		})
	}
}

func TestChoiceFromMessageRandom(t *testing.T) {
	bidwars, err := Parse([]byte(testJSON))
	if err != nil {
		t.Fatalf("error parsing test data: %v", err)
	}

	for _, tc := range []struct {
		desc  string
		msg   string
		wants []string // The ShortCodes of all Options that should appear when ChoiceFromMessage is run many times
	}{
		{"choose randomly", "random", []string{"Moo", "NBC", "DMC1", "DMC2", "DMC3"}},
		{"random directive is case-insensitive", "RANdoM", []string{"Moo", "NBC", "DMC1", "DMC2", "DMC3"}},
		{"accept specific option over random choice", "random but actually pick moo moo", []string{"Moo"}},
		{"'random' can appear in any substring", "please pick randomly", []string{"Moo", "NBC", "DMC1", "DMC2", "DMC3"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			gotCodes := make(map[string]int)
			for i := 0; i < 100; i++ {
				got := bidwars.ChoiceFromMessage(tc.msg, FromChatMessage)
				gotCodes[got.Option.ShortCode] += 1
			}
			gots := make([]string, 0, len(gotCodes))
			for key, _ := range gotCodes {
				gots = append(gots, key)
			}
			sort.Strings(gots)
			sort.Strings(tc.wants)
			if !reflect.DeepEqual(gots, tc.wants) {
				t.Errorf("got keys %v, want keys %v", gotCodes, tc.wants)
			}
		})
	}
}

func TestMakeChoice(t *testing.T) {
	vr := &sheets.ValueRange{
		Range:          "Tracker!A:E",
		MajorDimension: "ROWS",
		Values: [][]interface{}{
			{"Contributor", "What", "Points", "Choice", "Message"},
			{"aerionblue", "resub", "5.00"},
			{"AEWC20XX", "resub", "5.00"},
			{"aerionblue", "200 bits", "2.00", "", ""},
			{"aerionblue", "donation", "5.01", "Leon", "put this towards Leon"},
		},
	}
	choice := Choice{Option: Option{DisplayName: "Moo Moo Meadows", ShortCode: "Moo"}, Reason: "usedMoo"}

	for _, tc := range []struct {
		desc       string
		donor      string
		choice     Choice
		wantValues [][]interface{}
		wantRows   []donationRow
	}{
		{
			"updates one row",
			"AEWC20XX",
			choice,
			[][]interface{}{{}, {}, {nil, nil, nil, "Moo", "usedMoo"}, {}, {}},
			[]donationRow{vr.Values[2]},
		},
		{
			"updates all empty rows for donor",
			"aerionblue",
			choice,
			[][]interface{}{{}, {nil, nil, nil, "Moo", "usedMoo"}, {}, {nil, nil, nil, "Moo", "usedMoo"}, {}},
			[]donationRow{vr.Values[1], vr.Values[3]},
		},
		{
			"does not update header row",
			"Contributor",
			choice,
			[][]interface{}{{}, {}, {}, {}, {}},
			nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			gotVR, gotRows := makeChoice(vr, tc.donor, tc.choice)
			if gotVR.Range != vr.Range {
				t.Errorf("Range should be same as input: got %v, want %v", gotVR.Range, vr.Range)
			}
			if gotVR.MajorDimension != vr.MajorDimension {
				t.Errorf("MajorDimension should be same as input: got %v, want %v", gotVR.MajorDimension, vr.MajorDimension)
			}
			if diff := deep.Equal(gotVR.Values, tc.wantValues); diff != nil {
				t.Error(diff)
			}
			if diff := deep.Equal(gotRows, tc.wantRows); diff != nil {
				t.Error(diff)
			}
		})
	}
}

func TestTotalsToString_AllStyle(t *testing.T) {
	for _, tc := range []struct {
		desc        string
		centsTotals []int
		want        string
	}{
		{"two options", []int{1000, 994}, "Option 1: 10.00, Option 2: 9.94 (down by 0.06)"},
		{"two options, reversed", []int{994, 1000}, "Option 1: 9.94 (down by 0.06), Option 2: 10.00"},
		{"three options", []int{12345, 11037, 10000}, "Option 1: 123.45, Option 2: 110.37 (down by 13.08), Option 3: 100.00 (down by 23.45)"},
		{"three options, tie for lead", []int{500, 150, 500}, "Option 1: 5.00, Option 2: 1.50 (down by 3.50), Option 3: 5.00"},
		{"one option", []int{999}, "Option 1: 9.99"},
		{"zero options", []int{}, ""},
	} {
		var totals []Total
		for n, cents := range tc.centsTotals {
			totals = append(totals, Total{
				Option: Option{DisplayName: fmt.Sprintf("Option %d", n+1)},
				Value:  donation.CentsValue(cents),
			})
		}
		t.Run(tc.desc, func(t *testing.T) {
			got := Totals{totals: totals}.Describe(Option{})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTotalsToString_LastPlaceStyle(t *testing.T) {
	for _, tc := range []struct {
		desc        string
		centsTotals []int
		lastBid     string
		want        string
	}{
		{"two options", []int{1000, 994}, "", "Last place: B (down by 0.06)"},
		{"two options, lastBid for leader", []int{1000, 994}, "A", "A is currently #1. Last place: B (down by 0.06)"},
		{"two options, lastBid for trailer", []int{1000, 994}, "B", "B is still in last place (down by 0.06) usedShame"},
		{"last place has $0", []int{0, 1000}, "", "Last place: A (down by 10.00)"},
		{"four options", []int{12345, 11037, 9000, 5000}, "", "Last place: D (down by 40.00)"},
		{"four options, lastBid on 1st place", []int{12345, 11037, 9000, 5000}, "A", "A is currently #1. Last place: D (down by 40.00)"},
		{"four options, lastBid on 2nd place", []int{12345, 11037, 9000, 5000}, "B", "B is currently #2. Last place: D (down by 40.00)"},
		{"four options, lastBid on 3rd place", []int{12345, 11037, 9000, 5000}, "C", "C is currently #3. Last place: D (down by 40.00)"},
		{"four options, lastBid on 4th place", []int{12345, 11037, 9000, 5000}, "D", "D is still in last place (down by 40.00) usedShame"},
		{"tie for last place", []int{500, 150, 150}, "A", "A is currently #1. Tie for last place: B, C (down by 3.50)"},
		{"lastBid is tied for last", []int{500, 150, 150}, "B", "Tie for last place: B, C (down by 3.50)"},
		{"first options are tied for last", []int{150, 150, 500}, "", "Tie for last place: A, B (down by 3.50)"},
		{"all options tied", []int{150, 150, 150}, "", "Tie for last place: A, B, C (down by 0.00)"},
		{"lastBid is tied, but not last place", []int{500, 300, 300, 100}, "C", "C is currently #2. Last place: D (down by 2.00)"},
		{"one option", []int{999}, "", "A: 9.99"},
		{"one option with lastBid", []int{999}, "A", "A: 9.99"},
		{"zero options", []int{}, "", ""},
	} {
		var totals []Total
		var lastBidOption Option
		for n, cents := range tc.centsTotals {
			name := string(rune(int('A') + n))
			opt := Option{DisplayName: name, ShortCode: name}
			if opt.ShortCode == tc.lastBid {
				lastBidOption = opt
			}
			totals = append(totals, Total{
				Option: opt,
				Value:  donation.CentsValue(cents),
			})
		}
		t.Run(tc.desc, func(t *testing.T) {
			got := Totals{totals: totals, summaryStyle: "LAST_PLACE"}.Describe(lastBidOption)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTotalsToString_WinnersStyle(t *testing.T) {
	for _, tc := range []struct {
		desc        string
		winners     int
		centsTotals []int
		lastBid     string
		want        string
	}{
		{"top 2 out of 3", 2, []int{200, 100, 300}, "", "Current top 2: C, A"},
		{"top 3 out of 3", 3, []int{200, 100, 300}, "", "Current top 3: C, A, B"},
		{"2 of 3, lastBid for leader", 2, []int{200, 100, 300}, "C", "C is currently #1. Current top 2: C, A"},
		{"2 of 3, lastBid for trailer", 2, []int{200, 100, 300}, "B", "B is currently #3. Current top 2: C, A"},
		{"2 of 3, 2-way tie for #1", 2, []int{200, 300, 300}, "C", "C is currently #1. Current top 2: B, C"},
		{"2 of 3, 2-way tie for #2", 2, []int{200, 200, 300}, "B", "B is currently #2. Current top 2: C, A, B"},
		{"more winners than options", 10, []int{100, 300}, "", "Current top 10: B, A"},
		{"one option", 1, []int{999}, "", "A: 9.99"},
		{"one option with lastBid", 1, []int{999}, "A", "A: 9.99"},
		{"zero options", 1, []int{}, "", ""},
	} {
		var totals []Total
		var lastBidOption Option
		for n, cents := range tc.centsTotals {
			name := string(rune(int('A') + n))
			opt := Option{DisplayName: name, ShortCode: name}
			if opt.ShortCode == tc.lastBid {
				lastBidOption = opt
			}
			totals = append(totals, Total{
				Option: opt,
				Value:  donation.CentsValue(cents),
			})
		}
		t.Run(tc.desc, func(t *testing.T) {
			got := Totals{
				totals:          totals,
				summaryStyle:    "WINNERS",
				numberOfWinners: tc.winners,
			}.Describe(lastBidOption)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseJSONConfig_DefaultValues(t *testing.T) {
	bidwars, err := Parse([]byte(`{
	    "contests": [
	        {
	            "name": "test of default config values",
	            "options": [
	                {"displayName": "A", "shortCode": "A", "aliases": []},
	                {"displayName": "B", "shortCode": "B", "aliases": []}
	            ]
	        },
	        {
	            "name": "test that config values are overridden",
	            "summaryStyle": "WINNERS",
	            "numberOfWinners": 5,
	            "options": [
	                {"displayName": "A", "shortCode": "A", "aliases": []},
	                {"displayName": "B", "shortCode": "B", "aliases": []}
	            ]
	        }
	    ]
	}
	`))
	if err != nil {
		t.Errorf("parse failed")
	}
	if got := bidwars.Contests[0].SummaryStyle; got != "ALL" {
		t.Errorf("wrong default value of SummaryStyle: got %s, want ALL", got)
	}
	if got := bidwars.Contests[0].NumberOfWinners; got != 1 {
		t.Errorf("wrong default value of NumberOfWinners: got %d, want 1", got)
	}
	if got := bidwars.Contests[1].SummaryStyle; got != "WINNERS" {
		t.Errorf("wrong parsed value of SummaryStyle: got %s, want WINNERS", got)
	}
	if got := bidwars.Contests[1].NumberOfWinners; got != 5 {
		t.Errorf("wrong parsed value of NumberOfWinners: got %d, want 5", got)
	}
}
