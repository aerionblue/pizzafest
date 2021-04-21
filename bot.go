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
	ircClient         *twitch.Client
	ircRepliesEnabled bool
	dbRecorder        db.Recorder
	bidwars           bidwar.Collection
	bidwarTallier     *bidwar.Tallier

	mu sync.RWMutex
	// Maps a Twitch username to the last time they gave a community gift sub.
	communityGifts map[string]time.Time
	lastChatTime   time.Time
}

func (b *bot) dispatchSubEvent(ev donation.Event) {
	if ev.Type == donation.CommunityGift {
		b.updateCommunityGift(ev)
	}
	if ev.Type == donation.GiftSubscription && b.shouldIgnoreSubGift(ev) {
		return
	}
	log.Printf("new subscription by %v worth $%s (tier: %d, months: %d, count: %d)", ev.Owner, ev.Value(), ev.SubTier, ev.SubMonths, ev.SubCount)
	bid := b.bidwars.ChoiceFromMessage(ev.Message, bidwar.FromSubMessage)
	go func() {
		if err := b.dbRecorder.RecordDonation(ev, bid); err != nil {
			log.Printf("ERROR writing donation to db: %v", err)
			return
		}
		b.sayWithTotals(
			ev.Channel,
			bid.Option,
			fmt.Sprintf("@%s: I put your sub towards %s.", ev.Owner, bid.Option.DisplayName))
	}()
}

func (b *bot) dispatchBitsEvent(ev donation.Event) {
	log.Printf("new bits donation by %v worth $%s (bits: %d)", ev.Owner, ev.Value(), ev.Bits)
	bid := b.bidwars.ChoiceFromMessage(ev.Message, bidwar.FromChatMessage)
	go func() {
		if err := b.dbRecorder.RecordDonation(ev, bid); err != nil {
			log.Printf("ERROR writing donation to db: %v", err)
			return
		}
		b.sayWithTotals(
			ev.Channel,
			bid.Option,
			fmt.Sprintf("@%s: I put your bits towards %s.", ev.Owner, bid.Option.DisplayName))
	}()
}

func (b *bot) dispatchBidCommand(m twitch.PrivateMessage) {
	go func() {
		donor := m.User.Name
		updateStats, err := b.bidwarTallier.AssignFromMessage(donor, m.Message)
		if err != nil {
			log.Printf("ERROR assigning bid command for %s", donor)
			return
		}
		var msg string
		if updateStats.TotalValue.Cents() > 0 {
			msg = fmt.Sprintf("@%s: I put your $%s towards %s.",
				donor, updateStats.TotalValue, updateStats.Option.DisplayName)
		}
		b.sayWithTotals(m.Channel, updateStats.Option, msg)
	}()
}

func (b *bot) dispatchStreamlabsDonation(ev donation.Event) {
	log.Printf("new streamlabs donation by %v worth $%s (cash: %s)", ev.Owner, ev.Value(), ev.Cash)
	bid := b.bidwars.ChoiceFromMessage(ev.Message, bidwar.FromDonationMessage)
	go func() {
		if err := b.dbRecorder.RecordDonation(ev, bid); err != nil {
			log.Printf("ERROR writing donation to db: %v", err)
			return
		}
		b.sayWithTotals(
			ev.Channel,
			bid.Option,
			fmt.Sprintf("$%s donation from %s put towards %s.",
				ev.Value(), ev.Owner, bid.Option.DisplayName))
	}()
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

func (b *bot) getNewTotals(opt bidwar.Option) (bidwar.Totals, error) {
	contest := b.bidwars.FindContest(opt.DisplayName)
	if contest.Name == "" {
		return nil, fmt.Errorf("could not find bid war for option %q", opt.DisplayName)
	}
	totals, err := b.bidwarTallier.TotalsForContest(contest)
	if err != nil {
		return nil, fmt.Errorf("error fetching current bid war totals: %v", err)
	}
	return totals, nil
}

func (b *bot) say(channel string, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastChatTime.Add(chatCooldown).After(time.Now()) {
		log.Printf("[on cooldown for #%v] %v", channel, msg)
		return
	}
	log.Printf("[-> #%v] %v", channel, msg)
	b.lastChatTime = time.Now()
	if b.ircRepliesEnabled {
		b.ircClient.Say(channel, msg)
	}
}

func (b *bot) sayWithTotals(channel string, opt bidwar.Option, msgPrefix string) {
	if opt.DisplayName == "" {
		return
	}
	totals, err := b.getNewTotals(opt)
	if err != nil {
		log.Printf("ERROR reading new bid war totals: %v", err)
		return
	}
	msg := totals.String()
	if msgPrefix != "" {
		msg = msgPrefix + " " + msg
	}
	b.say(channel, msg)
}

func doLocalTest(b *bot, channel string, ircClient *twitch.Client, tallier *bidwar.Tallier) {
	<-time.After(2 * time.Second)
	ircClient.Say(channel, "subgift --tier 2 --months 6 --username aerionblue --username2 AEWC20XX")
	ircClient.Say(channel, "submysterygift --username usedpizza --count 3")
	ircClient.Say(channel, "subgift --username aerionblue --username2 AEWC20XX")
	ircClient.Say(channel, "subgift --username usedpizza --username2 eldritchdildoes")
	ircClient.Say(channel, `bits --bitscount 444 --username "Mizalie" usedU`)
	ircClient.Say(channel, `bits --bitscount 250 --username "TWRoxas" shadows of the damned`)
	<-time.After(2 * time.Second)
	pm := twitch.PrivateMessage{
		User:    twitch.User{Name: "aerionblue"},
		Type:    twitch.PRIVMSG,
		Channel: "testing",
		Message: "!bid wind waker please",
	}
	b.dispatchBidCommand(pm)
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	twitchChatCredsPath := flag.String("twitch_chat_creds", "", "Path to the Twitch chat credentials file")
	twitchChatRepliesEnabled := flag.Bool("chat_replies_enabled", true, "Whether Twitch chat replies are enabled")
	firestoreCredsPath := flag.String("firestore_creds", "", "Path to the Firestore credentials file")
	sheetsCredsPath := flag.String("sheets_creds", "", "Path to the Google Sheets OAuth client secret file")
	sheetsTokenPath := flag.String("sheets_token", "", "Path to the Google Sheets OAuth token. If absent, you will be prompted to create a new token")
	streamlabsCredsPath := flag.String("streamlabs_creds", "", "Path to a Streamlabs OAuth token. If absent, Streamlabs donation checking will be disabled")
	bidWarDataPath := flag.String("bidwar_data", "", "Path to a JSON file describing the current bid wars")
	flag.Parse()

	var ircClient *twitch.Client
	ircRepliesEnabled := *twitchChatRepliesEnabled
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
		ircRepliesEnabled = false // Just echo replies to the log
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
		donationTable := googlesheets.NewDonationTable(sheetsSrv, spreadsheetID, bidTrackerSheetName)
		dbRecorder = db.NewGoogleSheetsClient(donationTable)
		bidwarTallier = bidwar.NewTallier(sheetsSrv, donationTable, spreadsheetID, bidwars)
		bidTotals, err := bidwarTallier.GetTotals()
		if err != nil {
			log.Fatalf("error reading current bid war totals: %v", err)
		}
		log.Printf("found %d bid war options in the database", len(bidTotals))
		for _, bt := range bidTotals {
			log.Printf("Current total for %q is %s", bt.Option.DisplayName, bt.Value)
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
		donationPoller, err = streamlabs.NewDonationPoller(context.Background(), *streamlabsCredsPath, *targetChannel)
		if err != nil {
			log.Printf("(non-fatal) error initializing Streamlabs polling: %v", err)
		}
	} else {
		log.Print("no Streamlabs token provided")
	}

	b := &bot{
		ircClient:         ircClient,
		ircRepliesEnabled: ircRepliesEnabled,
		dbRecorder:        dbRecorder,
		bidwars:           bidwars,
		bidwarTallier:     bidwarTallier,
		communityGifts:    make(map[string]time.Time),
	}

	ircClient.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		if ev, ok := donation.ParseSubEvent(m); ok {
			b.dispatchSubEvent(ev)
		}
	})
	ircClient.OnPrivateMessage(func(m twitch.PrivateMessage) {
		if ev, ok := donation.ParseBitsEvent(m); ok {
			b.dispatchBitsEvent(ev)
		} else if strings.HasPrefix(strings.ToLower(m.Message), bidCommand) {
			b.dispatchBidCommand(m)
		}
	})
	ircClient.Join(*targetChannel)

	if donationPoller != nil {
		donationPoller.OnDonation(func(ev donation.Event) {
			b.dispatchStreamlabsDonation(ev)
		})
		if err := donationPoller.Start(); err != nil {
			log.Fatalf("Streamlabs polling error: %v", err)
		}
	}

	if !*prod {
		go doLocalTest(b, *targetChannel, ircClient, bidwarTallier)
	}

	log.Print("connecting to IRC...")
	if err := ircClient.Connect(); err != nil {
		panic(err)
	}
}
