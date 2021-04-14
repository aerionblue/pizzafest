package bidwar

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"google.golang.org/api/sheets/v4"
)

// Google Sheets developer metadata keys. The target spreadsheet must contain
// metadata with these keys, located at the appropriate columns of the bid war
// tracker sheet. You'll need to use a separate script to send
// CreateDeveloperMetadata requests to the API in order to set this up.
const metadataBidWarNames = "bidWarNames"
const metadataBidWarTotals = "bidWarTotals"

// Collection is a set of bid wars.
type Collection struct {
	Contests []Contest
}

// Contest is a single bid war between several options. The option that
// receives the most money will win this contest.
type Contest struct {
	Name    string
	Options []Option
}

// Option is a contestant in a bid war. Donors can allocate money to an option
// to help it win its bid war.
type Option struct {
	DisplayName string
	// All the aliases by which this choice is known. Matching any of these
	// aliases in a donation message designates the money to this choice.
	Aliases []alias
}

// Choice is a choice that a donor made for the bid war.
type Choice struct {
	Option Option // The donor's chosen Option.
	Reason string // The reason we allocated the donation to the Option.
}

type ChoiceReason int

const (
	FromChatMessage ChoiceReason = iota
	FromDonationMessage
	FromSubMessage
)

// ChoiceFromMessage determines whether the given donation message or chat
// message mentioned one of the bid war options in this Collection, and returns
// a Choice representing that Option. If no bid war option was found, returns
// the zero value. If more than one Option matches, returns the match that
// occurs earliest (leftmost) in the message.
func (c Collection) ChoiceFromMessage(msg string, reason ChoiceReason) Choice {
	minIndex := -1
	minOpt := Option{}
	for _, con := range c.Contests {
		for _, opt := range con.Options {
			for _, a := range opt.Aliases {
				if loc := a.FindStringIndex(msg); loc != nil {
					idx := loc[0]
					if minIndex > idx || minIndex < 0 {
						minIndex = idx
						minOpt = opt
					}
				}
			}
		}
	}
	return Choice{Option: minOpt, Reason: reasonString(reason, msg)}
}

func reasonString(reason ChoiceReason, msg string) string {
	if msg == "" {
		return ""
	}
	switch reason {
	case FromChatMessage:
		return "[chat] " + msg
	case FromDonationMessage:
		return "[donation msg] " + msg
	case FromSubMessage:
		return "[sub msg] " + msg
	}
	return ""
}

type alias struct {
	*regexp.Regexp
}

func (a *alias) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	// (?i) = case-insensitive; \b = ASCII word boundary
	r, err := regexp.Compile(fmt.Sprintf(`(?i)\b%s\b`, s))
	if err != nil {
		return fmt.Errorf("alias %v not suitable for regexp: %v", s, err)
	}
	a.Regexp = r
	return nil
}

func Parse(rawJson []byte) (Collection, error) {
	var c Collection
	if err := json.Unmarshal(rawJson, &c); err != nil {
		return Collection{}, err
	}
	return c, nil
}

// Total is the total money contributed towards the given bid war Option.
type Total struct {
	Option Option
	Cents  int // The total number of US cents contributed towards this option.
}

// Tallier assigns donations to bid war options and reports bid totals.
type Tallier struct {
	sheetsSrv     *sheets.Service
	spreadsheetID string
	collection    Collection
}

// NewTallier creates a Tallier.
func NewTallier(srv *sheets.Service, spreadsheetID string, collection Collection) *Tallier {
	return &Tallier{srv, spreadsheetID, collection}
}

// GetTotals looks up the current total for each bid war Option. The totals
// are returned in arbitrary order.
func (t Tallier) GetTotals() ([]Total, error) {
	getReq := &sheets.BatchGetValuesByDataFilterRequest{
		DataFilters: []*sheets.DataFilter{
			{
				DeveloperMetadataLookup: &sheets.DeveloperMetadataLookup{
					MetadataKey: metadataBidWarNames,
				},
			},
			{
				DeveloperMetadataLookup: &sheets.DeveloperMetadataLookup{
					MetadataKey: metadataBidWarTotals,
				},
			},
		},
		MajorDimension: "COLUMNS",
	}
	getResp, err := t.sheetsSrv.Spreadsheets.Values.BatchGetByDataFilter(t.spreadsheetID, getReq).Do()
	if err != nil {
		return nil, err
	}

	var rawNames, rawTotals []interface{}
	for _, vr := range getResp.ValueRanges {
		for _, df := range vr.DataFilters {
			if df.DeveloperMetadataLookup.MetadataKey == metadataBidWarNames {
				rawNames = vr.ValueRange.Values[0]
				continue
			}
			if df.DeveloperMetadataLookup.MetadataKey == metadataBidWarTotals {
				rawTotals = vr.ValueRange.Values[0]
				continue
			}
		}
	}

	optsMap := make(map[string]Option)
	for _, contest := range t.collection.Contests {
		for _, option := range contest.Options {
			optsMap[option.DisplayName] = option
		}
	}

	var totals []Total
	for i := 0; i < len(rawNames) && i < len(rawTotals); i++ {
		if rawTotals[i] != "" {
			var n, v string
			var ok bool
			if n, ok = rawNames[i].(string); !ok {
				return nil, fmt.Errorf("expected string, got %T for value %v", rawNames[i], rawNames[i])
			}
			if v, ok = rawTotals[i].(string); !ok {
				return nil, fmt.Errorf("expected string, got %T for value %v", rawTotals[i], rawTotals[i])
			}
			var opt Option
			if opt, ok = optsMap[n]; !ok {
				continue
			}
			dollars, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid total for %v: %v", n, v)
			}
			totals = append(totals, Total{Option: opt, Cents: int(dollars * 100)})
		}
	}
	return totals, nil
}
