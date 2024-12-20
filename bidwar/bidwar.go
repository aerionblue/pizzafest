package bidwar

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/api/sheets/v4"

	"github.com/aerionblue/pizzafest/donation"
	"github.com/aerionblue/pizzafest/googlesheets"
)

// Google Sheets developer metadata keys. The target spreadsheet must contain
// metadata with these keys, located at the appropriate columns of the bid war
// tracker sheet. You'll need to use a separate script to send
// CreateDeveloperMetadata requests to the API in order to set this up.
const metadataBidWarNames = "bidWarNames"
const metadataBidWarTotals = "bidWarTotals"

// Special directives users can use when selecting a bid war option.
var randomDirective = regexp.MustCompile("(?i)random")

// Collection is a set of bid wars.
type Collection struct {
	Contests []Contest
	// Whether to ONLY accept bids via explicit chat command. Defaults to
	// false, i.e., bids will be inferred from resub messages, etc.
	RequireExplicitBid bool
}

// Contest is a single bid war between several options. The option that
// receives the most money will win this contest.
type Contest struct {
	// Display name for the contest.
	Name string
	// How to summarize the totals. This doesn't affect bid tallying behavior.
	// It only changes how the current status of the bid war is reported to users.
	// The default is "ALL": all options are reported, in descending order (i.e.,
	// winning option first).
	// TODO(aerion): Enum-ify this.
	SummaryStyle string
	// How many of the options will win. Only used if the summary style
	// is "WINNERS".
	NumberOfWinners int
	// The options on which donors can bid money.
	Options []Option
	// Whether this contest is accepting new bids.
	Closed bool
}

func (c *Contest) UnmarshalJSON(data []byte) error {
	type withDefaults Contest
	newC := &withDefaults{
		SummaryStyle:    "ALL",
		NumberOfWinners: 1,
	}
	if err := json.Unmarshal(data, newC); err != nil {
		return err
	}
	*c = Contest(*newC)
	return nil
}

// Option is a contestant in a bid war. Donors can allocate money to an option
// to help it win its bid war.
type Option struct {
	// The display name used when reporting bid war totals to users.
	DisplayName string
	// The short code used for bid war tracking. Must be unique in any Collection.
	ShortCode string
	// All the aliases by which this choice is known. Matching any of these
	// aliases in a donation message designates the money to this choice.
	Aliases []alias
	// Whether this option is closed to new bids. Bids for closed options will
	// be ignored.
	Closed bool
}

func (o Option) IsZero() bool {
	return o.ShortCode == ""
}

// Choice is a choice that a donor made for the bid war.
type Choice struct {
	Option Option // The donor's chosen Option.
	Reason string // The reason we allocated the donation to the Option.
}

type ChoiceReason int

const (
	// The bid choice was read from a chat message with money attached.
	FromChatMessage ChoiceReason = iota
	// The bid choice was read from a non-chat donation message.
	FromDonationMessage
	// The bid choice was read from a chat message in a resub notice.
	FromSubMessage
	// The bid choice was read from an explicit !bid command.
	FromBidCommand
)

// AllOpenOptions returns a list of all open Options in all open Contests.
func (c Collection) AllOpenOptions() []Option {
	var opts []Option
	for _, con := range c.Contests {
		if con.Closed {
			continue
		}
		for _, opt := range con.Options {
			if opt.Closed {
				continue
			}
			opts = append(opts, opt)
		}
	}
	return opts
}

// ChoiceFromMessage determines whether the given donation message or chat
// message mentioned one of the bid war options in this Collection, and
// returns a Choice representing that Option. If no bid war option was found,
// returns a Choice with the zero Option (but possibly non-zero Reason). If
// more than one Option matches, returns the match that occurs earliest
// (leftmost) in the message.
func (c Collection) ChoiceFromMessage(msg string, reason ChoiceReason) Choice {
	if c.RequireExplicitBid && reason != FromBidCommand {
		return Choice{}
	}
	minIndex := -1
	minOpt := Option{}
	openOptions := c.AllOpenOptions()
	for _, opt := range openOptions {
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
	if minIndex < 0 && randomDirective.MatchString(msg) {
		randIdx := rand.Intn(len(openOptions))
		minOpt = openOptions[randIdx]
	}
	return Choice{Option: minOpt, Reason: reasonString(reason, msg)}
}

// FindContest returns the open Contest that contains the given Option. If no
// Contest is matched, or if only closed Contests are matched, the zero
// Contest is returned.
func (c Collection) FindContest(o Option) Contest {
	for _, con := range c.Contests {
		if con.Closed {
			continue
		}
		for _, opt := range con.Options {
			if opt.ShortCode == o.ShortCode {
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
	Value  donation.CentsValue
}

type byCents []Total

func (b byCents) Len() int           { return len(b) }
func (b byCents) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b byCents) Less(i, j int) bool { return b[i].Value.Cents() < b[j].Value.Cents() }

// Totals is a series of bid war Totals.
type Totals struct {
	totals          []Total
	summaryStyle    string
	numberOfWinners int
}

// Describe returns a human-readable summary of the bid war. The description
// will always mention the lastBid option, but may omit others for the sake
// of brevity.
func (tt Totals) Describe(lastBid Option) string {
	switch tt.summaryStyle {
	case "LAST_PLACE":
		return tt.describeLastPlace(lastBid)
	case "FIRST_PLACE":
		return tt.describeFirstPlace(lastBid)
	case "WINNERS":
		if tt.numberOfWinners == 1 {
			return tt.describeFirstPlace(lastBid)
		}
		return tt.describeWinners(lastBid)
	case "ALL":
	}
	return tt.describeAll()
}

func (tt Totals) openTotals() []Total {
	var o []Total
	for _, t := range tt.totals {
		if !t.Option.Closed {
			o = append(o, t)
		}
	}
	return o
}

func (tt Totals) describeAll() string {
	maxValue := donation.CentsValue(0)
	for _, t := range tt.openTotals() {
		if t.Value > maxValue {
			maxValue = t.Value
		}
	}
	var totalStrs []string
	for _, t := range tt.openTotals() {
		s := fmt.Sprintf("%s: %s", t.Option.DisplayName, t.Value)
		if t.Value < maxValue {
			s += fmt.Sprintf(" (down by %s)", maxValue-t.Value)
		}
		totalStrs = append(totalStrs, s)
	}
	return strings.Join(totalStrs, ", ")
}

type optionRank struct {
	// The rank that these options occupy, with 1 being the most valuable.
	rank int
	// One or more options. These options are all tied for the specified rank.
	options []Option
	// The monetary. Every Option has this same value.
	value donation.CentsValue
}

// Returns all open Options and their ordinal ranks, ordered from highest
// value to lowest value. Options with equal values are returned in the same
// optionRank.
func (tt Totals) computeRanks() []*optionRank {
	openTotals := tt.openTotals()
	if len(openTotals) == 0 {
		return nil
	}

	sort.Sort(sort.Reverse(byCents(openTotals)))
	var ranks []*optionRank
	for idx, t := range openTotals {
		if ranks == nil || ranks[len(ranks)-1].value != t.Value {
			ranks = append(ranks, &optionRank{
				rank:  idx + 1,
				value: t.Value,
			})
		}
		lastRank := ranks[len(ranks)-1]
		lastRank.options = append(lastRank.options, t.Option)
	}
	return ranks
}

func (tt Totals) describeLastPlace(lastBid Option) string {
	ranks := tt.computeRanks()
	if len(ranks) == 0 {
		return ""
	} else if len(ranks) == 1 {
		if opts := ranks[0].options; len(opts) == 1 {
			return fmt.Sprintf("%s: %s", opts[0].DisplayName, ranks[0].value)
		}
	}

	lastPlaceRank := ranks[len(ranks)-1]
	diff := donation.CentsValue(0)
	if len(ranks) > 1 {
		diff = ranks[len(ranks)-2].value - lastPlaceRank.value
	}

	desc := "Last place: "
	if len(lastPlaceRank.options) > 1 {
		desc = "Tie for last place: "
	}
	var lastPlaceOptNames []string
	for _, opt := range lastPlaceRank.options {
		lastPlaceOptNames = append(lastPlaceOptNames, opt.DisplayName)
	}
	desc += fmt.Sprintf("%s (down by %s)", strings.Join(lastPlaceOptNames, ", "), diff)
	if lastBid.IsZero() {
		return desc
	}

	lastBidRank := findRankForBid(ranks, lastBid)
	if lastBidRank == nil {
		return desc
	}
	lastBidIsLastPlace := lastBidRank.rank == lastPlaceRank.rank
	// A special message for when the bidder's choice was in last place, and
	// remains alone in last place despite their efforts.
	if len(lastPlaceRank.options) == 1 && lastBidIsLastPlace {
		return fmt.Sprintf("%s is still in last place (down by %s) usedShame", lastBid.DisplayName, diff)
	}
	if lastBidIsLastPlace {
		return desc
	}
	return fmt.Sprintf("%s is currently #%d. %s", lastBid.DisplayName, lastBidRank.rank, desc)
}

func (tt Totals) describeFirstPlace(lastBid Option) string {
	ranks := tt.computeRanks()
	if len(ranks) == 0 {
		return ""
	} else if len(ranks) == 1 {
		if opts := ranks[0].options; len(opts) == 1 {
			return fmt.Sprintf("%s: %s", opts[0].DisplayName, ranks[0].value)
		}
	}

	firstPlaceRank := ranks[0]
	diff := donation.CentsValue(0)
	if len(ranks) > 1 {
		diff = firstPlaceRank.value - ranks[1].value
	}

	desc := "First place: "
	if len(firstPlaceRank.options) > 1 {
		desc = "Tie for first place: "
	}
	var firstPlaceOptNames []string
	for _, opt := range firstPlaceRank.options {
		firstPlaceOptNames = append(firstPlaceOptNames, opt.DisplayName)
	}
	desc += fmt.Sprintf("%s (up by %s)", strings.Join(firstPlaceOptNames, ", "), diff)
	if lastBid.IsZero() {
		return desc
	}

	lastBidRank := findRankForBid(ranks, lastBid)
	if lastBidRank == nil {
		return desc
	}
	lastBidIsFirstPlace := lastBidRank.rank == firstPlaceRank.rank
	// A special message for when the bidder's choice is alone in first place.
	if len(firstPlaceRank.options) == 1 && lastBidIsFirstPlace {
		return fmt.Sprintf("%s is in first place (up by %s) usedU", lastBid.DisplayName, diff)
	}
	if lastBidIsFirstPlace {
		return desc
	}
	return fmt.Sprintf("%s is currently #%d. %s", lastBid.DisplayName, lastBidRank.rank, desc)
}

func (tt Totals) describeWinners(lastBid Option) string {
	ranks := tt.computeRanks()
	if len(ranks) == 0 {
		return ""
	} else if len(ranks) == 1 {
		if opts := ranks[0].options; len(opts) == 1 {
			return fmt.Sprintf("%s: %s", opts[0].DisplayName, ranks[0].value)
		}
	}

	var leadingOptNames []string
	for _, r := range ranks {
		for _, opt := range r.options {
			leadingOptNames = append(leadingOptNames, opt.DisplayName)
		}
		if len(leadingOptNames) >= tt.numberOfWinners {
			break
		}
	}

	desc := fmt.Sprintf("Current top %d: %s", tt.numberOfWinners, strings.Join(leadingOptNames, ", "))
	if lastBid.IsZero() {
		return desc
	}
	lastBidRank := findRankForBid(ranks, lastBid)
	if lastBidRank == nil {
		return desc
	}
	return fmt.Sprintf("%s is currently #%d. %s", lastBid.DisplayName, lastBidRank.rank, desc)
}

func findRankForBid(ranks []*optionRank, bid Option) *optionRank {
	for _, r := range ranks {
		for _, opt := range r.options {
			if opt.ShortCode == bid.ShortCode {
				return r
			}
		}
	}
	return nil
}

// UpdateStats summarizes the changes made to a bid war.
type UpdateStats struct {
	Choice     Choice
	Count      int
	TotalValue donation.CentsValue
}

// Tallier assigns donations to bid war options and reports bid totals.
type Tallier struct {
	sheetsSrv     *sheets.Service
	table         *googlesheets.DonationTable
	spreadsheetID string
	collection    Collection
}

// NewTallier creates a Tallier.
func NewTallier(srv *sheets.Service, table *googlesheets.DonationTable, spreadsheetID string, collection Collection) *Tallier {
	return &Tallier{
		sheetsSrv:     srv,
		table:         table,
		spreadsheetID: spreadsheetID,
		collection:    collection,
	}
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
			optsMap[option.ShortCode] = option
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
			totals = append(totals, Total{
				Option: opt,
				Value:  donation.CentsValue(int(math.Round(dollars * 100))),
			})
		}
	}
	return totals, nil
}

// AssignFromMessage detects a donor's choice from a chat message and assigns
// the donor's previous bids to the chosen Option. If the message does not
// correspond to a known Option, returns the zero value (but no error).
func (t Tallier) AssignFromMessage(donor string, message string) (UpdateStats, error) {
	if donor == "" {
		return UpdateStats{}, errors.New("donor must not be empty")
	}
	choice := t.collection.ChoiceFromMessage(message, FromBidCommand)
	if choice.Option.IsZero() {
		return UpdateStats{}, nil
	}
	valueRange, err := t.table.GetTable()
	if err != nil {
		return UpdateStats{}, fmt.Errorf("error reading donation table: %v", err)
	}

	vrToWrite, matchedRows := makeChoice(valueRange, donor, choice)

	if len(matchedRows) > 0 {
		rowCount, err := t.table.WriteTable(vrToWrite)
		if err != nil {
			return UpdateStats{}, fmt.Errorf("error updating spreadsheet: %v", err)
		}
		log.Printf("updated %d rows for %s for %s", rowCount, donor, choice.Option.ShortCode)
	}

	totalCents := 0
	for _, dr := range matchedRows {
		totalCents += dr.Cents()
	}
	updateStats := UpdateStats{
		Choice:     choice,
		Count:      len(matchedRows),
		TotalValue: donation.CentsValue(totalCents),
	}

	return updateStats, nil
}

// TotalsForContest returns the current bid war total for each Option in a
// Contest, in descending order by value (i.e., the winning Option first).
func (t Tallier) TotalsForContest(contest Contest) (Totals, error) {
	totals, err := t.GetTotals()
	if err != nil {
		return Totals{}, err
	}
	optsByName := make(map[string]Option)
	for _, opt := range contest.Options {
		optsByName[opt.ShortCode] = opt
	}
	var totalsForContest []Total
	for _, tot := range totals {
		if _, ok := optsByName[tot.Option.ShortCode]; ok {
			totalsForContest = append(totalsForContest, tot)
		}
	}
	sort.Sort(sort.Reverse(byCents(totalsForContest)))
	return Totals{
		totals:          totalsForContest,
		summaryStyle:    contest.SummaryStyle,
		numberOfWinners: contest.NumberOfWinners,
	}, nil
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
		if strings.EqualFold(dr.Contributor(), donor) && dr.Choice() == "" {
			newRow = rowForChoice(choice)
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
		cents = int(math.Round(f * 100))
	case float64:
		cents = int(math.Round(v * 100))
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

func rowForChoice(choice Choice) donationRow {
	return []interface{}{nil, nil, nil, choice.Option.ShortCode, choice.Reason}
}
