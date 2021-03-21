package main

import (
	"context"
	"flag"
	"log"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v2"

	"github.com/aerionblue/pizzafest/db"
	"github.com/aerionblue/pizzafest/donation"
)

const testIRCAddress = "irc.fdgt.dev:6667"

// Hard-coded to a testing sheet, for now.
const spreadsheetID = "192vz0Kskkcv3vGuCnRDLlpdwc8_1fuU4Am5g7M7YrO8"
const bidTrackerSheetName = "Bid war worksheet"

type bot struct {
	ircClient  *twitch.Client
	dbRecorder db.Recorder
}

func (b *bot) dispatchUserNoticeMessage(m twitch.UserNoticeMessage) {
	ev, ok := donation.ParseSubEvent(m)
	if !ok {
		return
	}
	// TODO(aerion): Batch up multiple sub gifts. Maybe the answer here is to catch the community sub event and then ignore all gift sub events for a certain period of time.
	log.Printf("new subscription by %v worth %d dollars (tier: %d, months: %d, count: %d)", ev.Owner, ev.DollarValue(), ev.SubTier, ev.SubMonths, ev.SubCount)
	if err := b.dbRecorder.RecordDonation(ev); err != nil {
		log.Printf("ERROR writing sub to db: %v", err)
	}
}

func (b *bot) dispatchPrivateMessage(m twitch.PrivateMessage) {
	ev, ok := donation.ParseBitsEvent(m)
	if !ok {
		return
	}
	log.Printf("new bits donation by %v worth %d dollars (bits: %d)", ev.Owner, ev.DollarValue(), ev.Bits)
	if err := b.dbRecorder.RecordDonation(ev); err != nil {
		log.Printf("ERROR writing bits donation to db: %v", err)
	}
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	firestoreCredsPath := flag.String("firestore_creds", "", "Path to the Firestore credentials file")
	sheetsCredsPath := flag.String("sheets_creds", "", "Path to the Google Sheets OAuth client secret file")
	sheetsTokenPath := flag.String("sheets_token", "", "Path to the Google Sheets OAuth token. If absent, you will be prompted to create a new token")
	flag.Parse()

	ircClient := twitch.NewAnonymousClient()
	ircClient.Capabilities = []string{twitch.CommandsCapability, twitch.TagsCapability}
	if *prod {
		log.Printf("*** CONNECTING TO PROD #%s ***", *targetChannel)
	} else {
		log.Printf("--- connecting to fdgt #%s ---", *targetChannel)
		ircClient.IrcAddress = testIRCAddress
		ircClient.TLS = false
	}

	var dbRecorder db.Recorder
	if *sheetsCredsPath != "" {
		cfg := db.SheetsClientConfig{
			SpreadsheetID:    spreadsheetID,
			SheetName:        bidTrackerSheetName,
			ClientSecretPath: *sheetsCredsPath,
			OAuthTokenPath:   *sheetsTokenPath,
		}
		var err error
		dbRecorder, err = db.NewGoogleSheetsClient(context.Background(), cfg)
		if err != nil {
			log.Fatalf("error initializing Google Sheets client: %v", err)
		}
	} else if *firestoreCredsPath != "" {
		var err error
		dbRecorder, err = db.NewFirestoreClient(context.Background(), *firestoreCredsPath)
		if err != nil {
			log.Fatalf("error connecting to Firestore: %v", err)
		}
	} else {
		log.Fatal("no DB config specified; you must provide either Firestore or Google Sheets flags")
	}

	b := &bot{ircClient, dbRecorder}

	ircClient.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		b.dispatchUserNoticeMessage(m)
	})
	ircClient.OnPrivateMessage(func(m twitch.PrivateMessage) {
		b.dispatchPrivateMessage(m)
	})
	ircClient.Join(*targetChannel)

	if !*prod {
		go func() {
			<-time.After(2 * time.Second)
			ircClient.Say(*targetChannel, "subgift --tier 2 --months 6 --username usedpizza --username2 AEWC20XX")
			ircClient.Say(*targetChannel, `bits --bitscount 250 --message "oh! it's slugma!" --username "TWRoxas"`)
			ircClient.Say(*targetChannel, `this is a fake bits message cheer6969`)
		}()
	}

	log.Print("connecting... ")
	if err := ircClient.Connect(); err != nil {
		panic(err)
	}
}
