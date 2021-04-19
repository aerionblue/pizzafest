package db

import (
	"fmt"

	"github.com/aerionblue/pizzafest/bidwar"
	"github.com/aerionblue/pizzafest/donation"
	"github.com/aerionblue/pizzafest/googlesheets"
)

type sheetsClient struct {
	table *googlesheets.DonationTable
}

func NewGoogleSheetsClient(table *googlesheets.DonationTable) *sheetsClient {
	return &sheetsClient{table}
}

func (c *sheetsClient) RecordDonation(ev donation.Event, bid bidwar.Choice) error {
	err := c.table.Append(ev, bid.Option.DisplayName, bid.Reason)
	if err != nil {
		return fmt.Errorf("error appending data to sheet: %v", err)
	}
	return nil
}
