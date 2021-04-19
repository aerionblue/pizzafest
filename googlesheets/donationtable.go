package googlesheets

import (
	"fmt"
	"sync"

	"google.golang.org/api/sheets/v4"

	"github.com/aerionblue/pizzafest/donation"
)

type DonationTable struct {
	spreadsheetID string
	tableRange    string

	// mu must be held when performing any modification to the spreadsheet.
	mu  sync.Mutex
	srv *sheets.SpreadsheetsService
}

func NewDonationTable(srv *sheets.Service, spreadsheetID string, sheetName string) *DonationTable {
	// TODO(aerion): Escape this, in case the sheet name contains a single quote.
	tableRange := fmt.Sprintf("'%s'!A:E", sheetName)
	return &DonationTable{
		spreadsheetID: spreadsheetID,
		tableRange:    tableRange,
		srv:           srv.Spreadsheets,
	}
}

// Append adds a new donation to the end of the donation table.
func (dt *DonationTable) Append(ev donation.Event, bidwarOption string, bidwarReason string) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	call := dt.srv.Values.Append(dt.spreadsheetID, dt.tableRange, &sheets.ValueRange{
		Values: [][]interface{}{
			{
				ev.Owner,
				ev.Description(),
				fmt.Sprintf("%0.2f", float64(ev.CentsValue())/100),
				bidwarOption,
				bidwarReason,
			},
		},
	})
	// We use OVERWRITE so that formula cells next to the table are preserved.
	// When INSERT_ROWS inserts a row into the table, those formula cells are
	// left empty.
	call.InsertDataOption("OVERWRITE").ValueInputOption("USER_ENTERED")
	if _, err := call.Do(); err != nil {
		return err
	}
	return nil
}

// GetTable returns the entire donation table, including header.
func (dt *DonationTable) GetTable() (*sheets.ValueRange, error) {
	return dt.srv.Values.
		Get(dt.spreadsheetID, dt.tableRange).
		MajorDimension("ROWS").
		ValueRenderOption("UNFORMATTED_VALUE").
		Do()
}

// WriteTable writes to the donation table and returns the number of rows
// updated. The ValueRange should have the same structure as the one returned
// from GetTable. Cells with a nil value are not overwritten.
func (dt *DonationTable) WriteTable(vr *sheets.ValueRange) (int, error) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	resp, err := dt.srv.Values.
		Update(dt.spreadsheetID, vr.Range, vr).
		ValueInputOption("RAW").
		Do()
	if err != nil {
		return 0, err
	}
	return int(resp.UpdatedRows), nil
}
