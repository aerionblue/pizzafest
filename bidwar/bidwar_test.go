package bidwar

import (
	"testing"
)

const testJSON = `{
    "contests": [
        {
            "name": "Mario Kart track",
            "options": [
                {"displayName": "Moo Moo Meadows", "aliases": ["moo", "moomoo"]},
                {"displayName": "Neo Bowser City", "aliases": ["neo", "nbc"]}
            ]
        },
        {
            "name": "Featuring Dante From The Devil May Cry Series",
            "options": [
                {"displayName": "Devil May Cry", "aliases": ["dmc", "dmc1"]},
                {"displayName": "Devil May Cry 2", "aliases": ["dmc2"]},
                {"displayName": "Devil May Cry 3", "aliases": ["dmc3"]}
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
		want string // The DisplayName of the wanted Option
	}{
		{"simple match", "put this towards moo moo meadows", "Moo Moo Meadows"},
		{"ignore surrounding punctiation", "i said 'moo...'", "Moo Moo Meadows"},
		{"returns earliest match", "nbc, no i meant moo moo", "Neo Bowser City"},
		{"matches are case-insensitive", "MoO!", "Moo Moo Meadows"},
		{"numbers are part of the word", "i pick dmc2", "Devil May Cry 2"},
		{"must match whole word", "go to dmc", "Devil May Cry"},
		{"substrings don't count", "dmca takedown", ""},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got := bidwars.ChoiceFromMessage(tc.msg, FromChatMessage)
			if got.Option.DisplayName != tc.want {
				t.Errorf("got %q, want %q", got.Option.DisplayName, tc.want)
			}
		})
	}
}
