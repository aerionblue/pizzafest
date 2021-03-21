package db

import (
	"context"
	"fmt"

	"google.golang.org/api/sheets/v4"

	"github.com/aerionblue/pizzafest/donation"
)

type sheetsClient struct {
	srv           *sheets.Service
	spreadsheetID string
	tableRange    string
}

type SheetsClientConfig struct {
	SpreadsheetID    string
	SheetName        string
	ClientSecretPath string
	OAuthTokenPath   string
}

func NewGoogleSheetsClient(ctx context.Context, cfg SheetsClientConfig) (*sheetsClient, error) {
	srv, err := newSheetsService(ctx, cfg.ClientSecretPath, cfg.OAuthTokenPath)
	if err != nil {
		return nil, fmt.Errorf("error creating Google Sheets client: %v", err)
	}
	// TODO(aerion): Escape this, in case the sheet name contains a single quote.
	tableRange := fmt.Sprintf("'%s'!A1:E1", cfg.SheetName)
	return &sheetsClient{srv, cfg.SpreadsheetID, tableRange}, nil
}

func (c *sheetsClient) RecordDonation(ev donation.Event) error {
	valuesSrv := sheets.NewSpreadsheetsValuesService(c.srv)
	call := valuesSrv.Append(c.spreadsheetID, c.tableRange, &sheets.ValueRange{
		Values: [][]interface{}{
			{ev.Owner, "", cellVal(ev.SubValue()), "", cellVal(ev.Bits)},
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
