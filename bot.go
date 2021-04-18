package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v2"

	"github.com/aerionblue/pizzafest/bidwar"
	"github.com/aerionblue/pizzafest/db"
	"github.com/aerionblue/pizzafest/donation"
	"github.com/aerionblue/pizzafest/googlesheets"
	"github.com/aerionblue/pizzafest/streamlabs"
	"github.com/aerionblue/pizzafest/twitchchat"
)

const testIRCAddress = "irc.fdgt.dev:6667"

const spreadsheetID = "192vz0Kskkcv3vGuCnRDLlpdwc8_1fuU4Am5g7M7YrO8" // This is the testing sheet ID
//const spreadsheetID = "1FkioQXOEAe3UylIjTUEpA-1nf0kJ4JD_dU9v2yBFdfE"  // This is the real sheet ID
const bidTrackerSheetName = "Bid war tracker"

const bidCommand = "!bid"

const chatCooldown = 5 * time.Second
const massGiftCooldown = 5 * time.Second

type bot struct {
	ircClient     *twitch.Client
	dbRecorder    db.Recorder
	bidwars       bidwar.Collection
	bidwarTallier *bidwar.Tallier

	mu sync.RWMutex
	// Maps a Twitch username to the last time they gave a community gift sub.
	communityGifts map[string]time.Time
	lastChatTime   time.Time
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
	log.Printf("new subscription by %v worth %d cents (tier: %d, months: %d, count: %d)", ev.Owner, ev.CentsValue(), ev.SubTier, ev.SubMonths, ev.SubCount)
	bid := b.bidwars.ChoiceFromMessage(ev.Message, bidwar.FromSubMessage)
	b.recordDonation(ev, bid)
}

func (b *bot) dispatchPrivateMessage(m twitch.PrivateMessage) {
	if ev, ok := donation.ParseBitsEvent(m); ok {
		log.Printf("new bits donation by %v worth %d cents (bits: %d)", ev.Owner, ev.CentsValue(), ev.Bits)
		bid := b.bidwars.ChoiceFromMessage(ev.Message, bidwar.FromChatMessage)
		b.recordDonation(ev, bid)
	} else if strings.HasPrefix(m.Message, bidCommand) {
		donor := m.User.Name
		totals, updateStats, err := b.bidwarTallier.AssignFromMessage(donor, m.Message)
		if err != nil {
			log.Printf("ERROR assigning bid command for %s", donor)
		}
		var totalStrs []string
		for _, t := range totals {
			totalStrs = append(totalStrs, fmt.Sprintf("%s: $%0.2f", t.Option.DisplayName, float64(t.Cents)/100))
		}
		if updateStats.TotalCents > 0 {
			b.reply(m, fmt.Sprintf("@%s: I put your $%.02f towards %s. New totals: %s",
				donor, float64(updateStats.TotalCents)/100, updateStats.Option.DisplayName, strings.Join(totalStrs, ", ")))
		} else {
			b.reply(m, fmt.Sprintf("Totals: %s", strings.Join(totalStrs, ", ")))
		}
	}
}

func (b *bot) dispatchStreamlabsDonation(ev donation.Event) {
	log.Printf("new streamlabs donation by %v worth %d cents (cents: %d)", ev.Owner, ev.CentsValue(), ev.Cents)
	bid := b.bidwars.ChoiceFromMessage(ev.Message, bidwar.FromDonationMessage)
	b.recordDonation(ev, bid)
}

func (b *bot) recordDonation(ev donation.Event, bid bidwar.Choice) {
	if err := b.dbRecorder.RecordDonation(ev, bid); err != nil {
		log.Printf("ERROR writing donation to db: %v", err)
	}
}

func (b *bot) updateCommunityGift(ev donation.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.communityGifts[ev.Owner] = time.Now()
}

func (b *bot) shouldIgnoreSubGift(ev donation.Event) bool {
	// Community gifts cause one event announcing the N-sub gift, and then N
	// individual gift sub events. We try to deduplicate the gift subs that occur
	// soon after a community gift event.
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.communityGifts[ev.Owner].Add(massGiftCooldown).After(time.Now())
}

func (b *bot) reply(pm twitch.PrivateMessage, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastChatTime.Add(chatCooldown).After(time.Now()) {
		return
	}
	b.lastChatTime = time.Now()
	log.Printf("[-> #%v] %v", pm.Channel, msg)
	b.ircClient.Say(pm.Channel, msg)
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	twitchChatCredsPath := flag.String("twitch_chat_creds", "", "Path to the Twitch chat credentials file")
	firestoreCredsPath := flag.String("firestore_creds", "", "Path to the Firestore credentials file")
	sheetsCredsPath := flag.String("sheets_creds", "", "Path to the Google Sheets OAuth client secret file")
	sheetsTokenPath := flag.String("sheets_token", "", "Path to the Google Sheets OAuth token. If absent, you will be prompted to create a new token")
	streamlabsCredsPath := flag.String("streamlabs_creds", "", "Path to a Streamlabs OAuth token. If absent, Streamlabs donation checking will be disabled")
	bidWarDataPath := flag.String("bidwar_data", "", "Path to a JSON file describing the current bid wars")
	flag.Parse()

	var ircClient *twitch.Client
	if *prod {
		log.Printf("*** CONNECTING TO PROD #%s ***", *targetChannel)
		chatCreds, err := twitchchat.ParseCreds(*twitchChatCredsPath)
		if err != nil {
			log.Fatal(err)
		}
		ircClient = twitch.NewClient(chatCreds.Username, chatCreds.OAuthToken)
	} else {
		log.Printf("--- connecting to fdgt #%s ---", *targetChannel)
		ircClient = twitch.NewAnonymousClient()
		ircClient.IrcAddress = testIRCAddress
		ircClient.TLS = false
	}
	ircClient.Capabilities = []string{twitch.CommandsCapability, twitch.TagsCapability}

	var bidwars bidwar.Collection
	if *bidWarDataPath != "" {
		var err error
		data, err := ioutil.ReadFile(*bidWarDataPath)
		if err != nil {
			log.Fatalf("could not read bid war data file: %v", err)
		}
		bidwars, err = bidwar.Parse(data)
		if err != nil {
			log.Fatalf("malformed bid war data file: %v", err)
		}
	}

	var dbRecorder db.Recorder
	var donationPoller *streamlabs.DonationPoller
	var bidwarTallier *bidwar.Tallier
	if *sheetsCredsPath != "" {
		var err error
		sheetsSrv, err := googlesheets.NewService(context.Background(), *sheetsCredsPath, *sheetsTokenPath)
		if err != nil {
			log.Fatalf("error initializing Google Sheets API: %v", err)
		}
		cfg := db.SheetsClientConfig{
			Service:       sheetsSrv,
			SpreadsheetID: spreadsheetID,
			SheetName:     bidTrackerSheetName,
		}
		dbRecorder = db.NewGoogleSheetsClient(cfg)
		bidwarTallier = bidwar.NewTallier(sheetsSrv, spreadsheetID, bidTrackerSheetName, bidwars)
		bidTotals, err := bidwarTallier.GetTotals()
		if err != nil {
			log.Fatalf("error reading current bid war totals: %v", err)
		}
		log.Printf("found %d bid war options in the database", len(bidTotals))
		for _, bt := range bidTotals {
			log.Printf("Current total for %q is %v cents", bt.Option.DisplayName, bt.Cents)
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

	b := &bot{
		ircClient:      ircClient,
		dbRecorder:     dbRecorder,
		bidwars:        bidwars,
		bidwarTallier:  bidwarTallier,
		communityGifts: make(map[string]time.Time),
	}

	ircClient.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		b.dispatchUserNoticeMessage(m)
	})
	ircClient.OnPrivateMessage(func(m twitch.PrivateMessage) {
		b.dispatchPrivateMessage(m)
	})
	ircClient.Join(*targetChannel)

	if donationPoller != nil {
		donationPoller.OnDonation(func(ev donation.Event) {
			b.dispatchStreamlabsDonation(ev)
		})
		donationPoller.Start()
	}

	if !*prod {
		go func() {
			<-time.After(2 * time.Second)
			ircClient.Say(*targetChannel, "subgift --tier 2 --months 6 --username aerionblue --username2 AEWC20XX")
			ircClient.Say(*targetChannel, "submysterygift --username usedpizza --count 3")
			ircClient.Say(*targetChannel, "subgift --username aerionblue --username2 AEWC20XX")
			ircClient.Say(*targetChannel, "subgift --username usedpizza --username2 eldritchdildoes")
			ircClient.Say(*targetChannel, "subgift --username usedpizza --username2 Mia_Khalifa")
			ircClient.Say(*targetChannel, `bits --bitscount 250 --username "TWRoxas" shadows of the damned`)
			<-time.After(2 * time.Second)
			log.Print("submitting !bid message...")
			totals, updateStats, err := bidwarTallier.AssignFromMessage("aerionblue", "!bid wind waker please")
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("assigned %d rows (%d cents) to %q", updateStats.Count, updateStats.TotalCents, updateStats.Option.DisplayName)
			for _, t := range totals {
				log.Printf("new total for %q is %0.2f", t.Option.DisplayName, float64(t.Cents)/100)
			}
		}()
	}

	log.Print("connecting... ")
	if err := ircClient.Connect(); err != nil {
		panic(err)
	}
}
