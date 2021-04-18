package bidwar

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
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

// FindContest returns the Contest that contains an Option with the given
// display name.
func (c Collection) FindContest(displayName string) Contest {
	for _, con := range c.Contests {
		for _, opt := range con.Options {
			if opt.DisplayName == displayName {
				return con
			}
		}
	}
	return Contest{}
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

type byCents []Total

func (b byCents) Len() int           { return len(b) }
func (b byCents) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b byCents) Less(i, j int) bool { return b[i].Cents < b[j].Cents }

// UpdateStats summarizes the changes made to a bid war.
type UpdateStats struct {
	Option     Option
	Count      int
	TotalCents int
}

// Tallier assigns donations to bid war options and reports bid totals.
type Tallier struct {
	sheetsSrv      *sheets.Service
	spreadsheetID  string
	donationsRange string
	collection     Collection
}

// NewTallier creates a Tallier.
func NewTallier(srv *sheets.Service, spreadsheetID string, sheetName string, collection Collection) *Tallier {
	// TODO(aerion): Escape this, in case the sheet name contains a single quote.
	donationsRange := fmt.Sprintf("'%s'!A:E", sheetName)
	return &Tallier{srv, spreadsheetID, donationsRange, collection}
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

// AssignFromMessage detects a donor's choice from a chat message, assigns the
// donor's previous bids to the chosen Option, and returns the new totals for
// the affected Contest. If the message does not correspond to a known Option,
// returns nil.
func (t Tallier) AssignFromMessage(donor string, message string) ([]Total, UpdateStats, error) {
	if donor == "" {
		return nil, UpdateStats{}, errors.New("donor must not be empty")
	}
	choice := t.collection.ChoiceFromMessage(message, FromChatMessage)
	if choice.Option.DisplayName == "" {
		return nil, UpdateStats{}, nil
	}
	contest := t.collection.FindContest(choice.Option.DisplayName)
	if contest.Name == "" {
		return nil, UpdateStats{}, fmt.Errorf("could not find bid war contest for option %q", choice.Option.DisplayName)
	}

	valueRange, err := t.sheetsSrv.Spreadsheets.Values.
		Get(t.spreadsheetID, t.donationsRange).
		MajorDimension("ROWS").
		ValueRenderOption("UNFORMATTED_VALUE").
		Do()
	if err != nil {
		return nil, UpdateStats{}, fmt.Errorf("error reading spreadsheet: %v", err)
	}

	vrToWrite, matchedRows := makeChoice(valueRange, donor, choice)

	if len(matchedRows) > 0 {
		updateResp, err := t.sheetsSrv.Spreadsheets.Values.
			Update(t.spreadsheetID, vrToWrite.Range, vrToWrite).
			ValueInputOption("RAW").
			Do()
		if err != nil {
			return nil, UpdateStats{}, fmt.Errorf("error updating spreadsheet: %v", err)
		}
		log.Printf("updated %d rows for %s for %s", updateResp.UpdatedRows, donor, choice.Option.DisplayName)
	}

	// TODO(aerion): Worth experimenting to see if there's a race between
	// writing to the sheet and reading the totals after. We could just read
	// first and then do the math locally.
	totals, err := t.GetTotals()
	if err != nil {
		return nil, UpdateStats{}, err
	}
	optsByName := make(map[string]Option)
	for _, opt := range contest.Options {
		optsByName[opt.DisplayName] = opt
	}
	var totalsForContest []Total
	for _, tot := range totals {
		if _, ok := optsByName[tot.Option.DisplayName]; ok {
			totalsForContest = append(totalsForContest, tot)
		}
	}
	sort.Sort(sort.Reverse(byCents(totalsForContest)))

	totalCents := 0
	for _, dr := range matchedRows {
		totalCents += dr.Cents()
	}
	updateStats := UpdateStats{
		Option:     choice.Option,
		Count:      len(matchedRows),
		TotalCents: totalCents,
	}

	return totalsForContest, updateStats, nil
}

// makeChoice decides which rows in the given ValueRange need to be edited in
// order to implement the requested choice. It returns two values: a new
// ValueRange describing how to update the spreadsheet, and a list of the
// original values of the spreadsheet rows to be updated. We update each row
// where the "Contributor" column matches the donor and the "Choice" column is
// not already set.
func makeChoice(vr *sheets.ValueRange, donor string, choice Choice) (*sheets.ValueRange, []donationRow) {
	newValues := make([][]interface{}, len(vr.Values))
	var updatedRows []donationRow
	for i, row := range vr.Values {
		var newRow []interface{}
		dr := donationRow(row)
		if dr.Contributor() == donor && dr.Choice() == "" {
			newRow = rowForChoice(choice.Option.DisplayName, choice.Reason)
			updatedRows = append(updatedRows, dr)
		} else {
			newRow = []interface{}{}
		}
		newValues[i] = newRow
	}
	newVR := &sheets.ValueRange{
		MajorDimension: vr.MajorDimension,
		Range:          vr.Range,
		Values:         newValues,
	}

	return newVR, updatedRows
}

// TODO(aerion): This is a little hacky for now. We could make this more
// structured in the future if it needs to be more resistant to changes in
// spreadsheet layout.
type donationRow []interface{}

func (d donationRow) Contributor() string {
	return d.column(0)
}

func (d donationRow) Cents() int {
	if len(d) < 3 {
		return 0
	}

	var cents int
	switch v := d[2].(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		cents = int(f * 100)
	case float64:
		cents = int(v * 100)
	}
	return cents
}

func (d donationRow) Choice() string {
	return d.column(3)
}

func (d donationRow) column(n int) string {
	if n >= len(d) {
		return ""
	}
	s, _ := d[n].(string)
	return s
}

func rowForChoice(donor string, message string) donationRow {
	return []interface{}{nil, nil, nil, donor, message}
}
