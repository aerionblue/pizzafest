// Package db writes events to a database.
package db

import (
	"github.com/aerionblue/pizzafest/bidwar"
	"github.com/aerionblue/pizzafest/donation"
)

type Recorder interface {
	RecordDonation(ev donation.Event, bid bidwar.Choice) error
}
