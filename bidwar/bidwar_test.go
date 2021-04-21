package bidwar

import (
	"testing"

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
