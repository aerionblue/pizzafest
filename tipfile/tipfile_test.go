package tipfile

import (
	"testing"
)

func TestParseTipLogLine(t *testing.T) {
	for _, tc := range []struct {
		desc    string
		msg     string
		want    logEntry
		wantErr bool
	}{
		{"donation", "id1;200;NutDealer;nut", logEntry{"id1", 200, "NutDealer", "nut"}, false},
		{"no message", "id1;11037;NutDealer;", logEntry{"id1", 11037, "NutDealer", ""}, false},
		{"too few fields", "id1;200", logEntry{"id1", 200, "", ""}, false},
		{"too many fields", "id1;200;NutDealer;hey lol ;)", logEntry{"id1", 200, "NutDealer", "hey lol ;)"}, false},
		{"blank", "", logEntry{}, false},
		{"malformed number", "id1;110x;NutDealer;comment", logEntry{}, true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := parseTipLogLine(tc.msg)
			if err != nil {
				if !tc.wantErr {
					t.Errorf("got error %q, want %+v", err, tc.want)
				}
				return
			}
			if tc.wantErr {
				t.Errorf("got %+v, want error", got)
				return
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
