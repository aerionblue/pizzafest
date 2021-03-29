package main

import (
	"context"
	"flag"
	"log"
	"sync"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v2"

	"github.com/aerionblue/pizzafest/db"
	"github.com/aerionblue/pizzafest/donation"
	"github.com/aerionblue/pizzafest/streamlabs"
)

const testIRCAddress = "irc.fdgt.dev:6667"

// Hard-coded to a testing sheet, for now.
const spreadsheetID = "192vz0Kskkcv3vGuCnRDLlpdwc8_1fuU4Am5g7M7YrO8"
const bidTrackerSheetName = "Bid war worksheet"

type bot struct {
	ircClient  *twitch.Client
	dbRecorder db.Recorder

	mu sync.RWMutex
	// Maps a Twitch username to the last time they gave a community gift sub.
	communityGifts map[string]time.Time
}

func (b *bot) dispatchUserNoticeMessage(m twitch.UserNoticeMessage) {
	ev, ok := donation.ParseSubEvent(m)
	if !ok {
		return
	}
	if ev.Type == donation.CommunityGift {
		b.updateCommunityGift(ev)
	}
	if ev.Type == donation.GiftSubscription && b.shouldIgnoreSubGift(ev) {
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

func (b *bot) dispatchStreamlabsDonation(ev donation.Event) {
	log.Printf("new streamlabs donation by %v worth %d dollars (cents: %d)", ev.Owner, ev.DollarValue(), ev.Cents)
	if err := b.dbRecorder.RecordDonation(ev); err != nil {
		log.Printf("ERROR writing streamlabs donation to db: %v", err)
	}
}

func (b *bot) updateCommunityGift(ev donation.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.communityGifts[ev.Owner] = time.Now()
}

func (b *bot) shouldIgnoreSubGift(ev donation.Event) bool {
	// Community gifts cause one event announcing the N-sub gift, and then N individual gift sub events. We try to deduplicate the gift subs that occur soon after a community gift event.
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.communityGifts[ev.Owner].Add(5 * time.Second).After(time.Now())
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	firestoreCredsPath := flag.String("firestore_creds", "", "Path to the Firestore credentials file")
	sheetsCredsPath := flag.String("sheets_creds", "", "Path to the Google Sheets OAuth client secret file")
	sheetsTokenPath := flag.String("sheets_token", "", "Path to the Google Sheets OAuth token. If absent, you will be prompted to create a new token")
	streamlabsCredsPath := flag.String("streamlabs_creds", "", "Path to a Streamlabs OAuth token. If absent, Streamlabs donation checking will be disabled")
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
	var donationPoller *streamlabs.DonationPoller
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
	if *streamlabsCredsPath != "" {
		var err error
		donationPoller, err = streamlabs.NewDonationPoller(context.Background(), *streamlabsCredsPath)
		if err != nil {
			log.Printf("(non-fatal) error initializing Streamlabs polling: %v", err)
		}
	} else {
		log.Print("no Streamlabs token provided")
	}

	b := &bot{ircClient: ircClient, dbRecorder: dbRecorder, communityGifts: make(map[string]time.Time)}

	ircClient.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		b.dispatchUserNoticeMessage(m)
	})
	ircClient.OnPrivateMessage(func(m twitch.PrivateMessage) {
		b.dispatchPrivateMessage(m)
	})
	ircClient.Join(*targetChannel)

	donationPoller.OnDonation(func(ev donation.Event) {
		b.dispatchStreamlabsDonation(ev)
	})

	if !*prod {
		go func() {
			<-time.After(2 * time.Second)
			ircClient.Say(*targetChannel, "subgift --tier 2 --months 6 --username usedpizza --username2 AEWC20XX")
			ircClient.Say(*targetChannel, "submysterygift --username usedpizza --count 3")
			ircClient.Say(*targetChannel, "subgift --username usedpizza --username2 AEWC20XX")
			ircClient.Say(*targetChannel, "subgift --username usedpizza --username2 eldritchdildoes")
			ircClient.Say(*targetChannel, "subgift --username usedpizza --username2 Mia_Khalifa")
			ircClient.Say(*targetChannel, `bits --bitscount 250 --message "oh! it's slugma!" --username "TWRoxas"`)
			ircClient.Say(*targetChannel, `this is a fake bits message cheer6969`)
		}()
	}

	log.Print("connecting... ")
	if err := ircClient.Connect(); err != nil {
		panic(err)
	}
}
