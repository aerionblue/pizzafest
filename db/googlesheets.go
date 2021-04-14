package db

import (
	"fmt"

	"google.golang.org/api/sheets/v4"

	"github.com/aerionblue/pizzafest/bidwar"
	"github.com/aerionblue/pizzafest/donation"
)

type sheetsClient struct {
	srv           *sheets.Service
	spreadsheetID string
	tableRange    string
}

type SheetsClientConfig struct {
	Service       *sheets.Service
	SpreadsheetID string
	SheetName     string
}

func NewGoogleSheetsClient(cfg SheetsClientConfig) *sheetsClient {
	// TODO(aerion): Escape this, in case the sheet name contains a single quote.
	tableRange := fmt.Sprintf("'%s'!A1:E1", cfg.SheetName)
	return &sheetsClient{cfg.Service, cfg.SpreadsheetID, tableRange}
}

func (c *sheetsClient) RecordDonation(ev donation.Event, bid bidwar.Choice) error {
	valuesSrv := sheets.NewSpreadsheetsValuesService(c.srv)
	call := valuesSrv.Append(c.spreadsheetID, c.tableRange, &sheets.ValueRange{
		Values: [][]interface{}{
			{
				ev.Owner,
				ev.Description(),
				fmt.Sprintf("%0.2f", float64(ev.CentsValue())/100),
				bid.Option.DisplayName,
				bid.Reason,
			},
		},
	})
	// We use OVERWRITE so that formula cells next to the table are preserved. When INSERT_ROWS inserts a row into the table, those formula cells are left empty.
	call.InsertDataOption("OVERWRITE").ValueInputOption("USER_ENTERED")
	if _, err := call.Do(); err != nil {
		return fmt.Errorf("error appending data to sheet: %v", err)
	}
	return nil
}

func cellVal(n int) interface{} {
	if n == 0 {
		return ""
	}
	return n
}
