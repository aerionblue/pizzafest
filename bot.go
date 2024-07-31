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

	"golang.org/x/time/rate"

	"github.com/aerionblue/pizzafest/bidwar"
	"github.com/aerionblue/pizzafest/db"
	"github.com/aerionblue/pizzafest/donation"
	"github.com/aerionblue/pizzafest/googlesheets"
	"github.com/aerionblue/pizzafest/streamelements"
	"github.com/aerionblue/pizzafest/streamlabs"
	"github.com/aerionblue/pizzafest/tipfile"
	"github.com/aerionblue/pizzafest/twitchchat"
)

const testIRCAddress = "irc.fdgt.dev:6667"

const bidCommand = "!bid"

// Rate limit parameters for outgoing chat messages.
const chatCooldown = 1 * time.Second
const chatBucketSize = 10

// How long we remember a user's !bid preference.
const bidPrefTTL = 3 * time.Minute

// How long we ignore individual gift sub events after a community gift.
const massGiftCooldown = 10 * time.Second

// The minimum value that we will acknowledge. Donations below this value are
// still logged, and still count towards the grand total. We just won't
// allocate them to bid wars or reply to them.
const minimumDonation = donation.CentsValue(100)

type bot struct {
	ircClient         *twitch.Client
	ircRepliesEnabled bool
	dbRecorder        db.Recorder
	bidwars           bidwar.Collection
	bidwarTallier     *bidwar.Tallier
	minimumDonation   donation.CentsValue
	chatLimiter       *rate.Limiter

	mu sync.RWMutex
	// Maps a Twitch username to the last time they gave a community gift sub.
	communityGifts map[string]time.Time
	// Maps a Twitch username to a bid war preference. When a user uses !bid but
	// has no donations to assign, we keep track of it for a few minutes just in
	// case the donation data was slow in getting to us.
	pendingBids map[string]*bidPreference
}

func (b *bot) dispatchSubEvent(ev donation.Event) {
	if ev.Type == donation.CommunityGift {
		b.updateCommunityGift(ev)
	}
	if ev.Type == donation.GiftSubscription && b.shouldIgnoreSubGift(ev) {
		return
	}
	log.Printf("new subscription by %v worth $%s (tier: %d, months: %d, count: %d)", ev.Owner, ev.Value(), ev.SubTier, ev.SubMonths, ev.SubCount)
	bid := b.getChoice(ev, bidwar.FromSubMessage)
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
	bid := b.getChoice(ev, bidwar.FromChatMessage)
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
		opt := updateStats.Choice.Option
		if opt.IsZero() {
			opts := b.bidwars.AllOpenOptions()
			if len(opts) > 0 {
				shortCodes := make([]string, len(opts))
				for i, o := range opts {
					shortCodes[i] = o.ShortCode
				}
				b.say(m.Channel, fmt.Sprintf("@%s: These are the options: %s", donor, strings.Join(shortCodes, ", ")))
			}
			return
		}
		var msg string
		if updateStats.TotalValue.Points() > 0 {
			msg = fmt.Sprintf("@%s: +%s for %s usedNice", donor, updateStats.TotalValue, opt.DisplayName)
		} else {
			b.rememberPref(donor, updateStats.Choice)
			msg = fmt.Sprintf("@%s: You had no points used7 but I'll remember your choice for a few minutes.", donor)
		}
		b.sayWithTotals(m.Channel, opt, msg)
	}()
}

func (b *bot) dispatchMoneyDonation(ev donation.Event) {
	log.Printf("new dolla donation by %v worth $%s (cash: %s)", ev.Owner, ev.Value(), ev.Cash)
	bid := b.getChoice(ev, bidwar.FromDonationMessage)
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

func (b *bot) getChoice(ev donation.Event, reason bidwar.ChoiceReason) bidwar.Choice {
	if ev.Value() < b.minimumDonation {
		return bidwar.Choice{}
	}
	choice := b.bidwars.ChoiceFromMessage(ev.Message, reason)
	if !choice.Option.IsZero() {
		return choice
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	donor := strings.ToLower(ev.Owner)
	pref, ok := b.pendingBids[donor]
	delete(b.pendingBids, donor)
	if !ok {
		return bidwar.Choice{}
	}
	if time.Now().After(pref.Expiration) {
		return bidwar.Choice{}
	}
	return pref.Choice
}

func (b *bot) rememberPref(username string, choice bidwar.Choice) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingBids[strings.ToLower(username)] = &bidPreference{Choice: choice, Expiration: time.Now().Add(bidPrefTTL)}
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
	contest := b.bidwars.FindContest(opt)
	if contest.Name == "" {
		return bidwar.Totals{}, fmt.Errorf("could not find bid war for option %q", opt.ShortCode)
	}
	totals, err := b.bidwarTallier.TotalsForContest(contest)
	if err != nil {
		return bidwar.Totals{}, fmt.Errorf("error fetching current bid war totals: %v", err)
	}
	return totals, nil
}

func (b *bot) say(channel string, msg string) {
	if !b.chatLimiter.Allow() {
		log.Printf("[on cooldown for #%v] %v", channel, msg)
		return
	}
	log.Printf("[-> #%v] %v", channel, msg)
	if b.ircRepliesEnabled {
		b.ircClient.Say(channel, msg)
	}
}

func (b *bot) sayWithTotals(channel string, opt bidwar.Option, msgPrefix string) {
	if opt.IsZero() {
		return
	}
	totals, err := b.getNewTotals(opt)
	if err != nil {
		log.Printf("ERROR reading new bid war totals: %v", err)
		return
	}
	msg := totals.Describe(opt)
	if msgPrefix != "" {
		msg = msgPrefix + " " + msg
	}
	b.say(channel, msg)
}

// bidPreference represents a bid war choice that somebody expressed in the past.
type bidPreference struct {
	Choice     bidwar.Choice
	Expiration time.Time
}

func firstTokenIs(message, target string) bool {
	tokens := strings.Split(message, " ")
	return len(tokens) > 0 && tokens[0] == target
}

func doLocalTest(b *bot, channel string, ircClient *twitch.Client, tallier *bidwar.Tallier) {
	<-time.After(2 * time.Second)
	ircClient.Say(channel, "subgift --tier 2 --months 6 --username aerionblue --username2 AEWC20XX")
	ircClient.Say(channel, "submysterygift --username usedpizza --count 3")
	ircClient.Say(channel, "subgift --username aerionblue --username2 AEWC20XX")
	ircClient.Say(channel, "subgift --username usedpizza --username2 eldritchdildoes")
	ircClient.Say(channel, `bits --bitscount 444 --username "Mizalie" usedU`)
	ircClient.Say(channel, `bits --bitscount 250 --username "TWRoxas" ride to hell`)
	ircClient.Say(channel, `bits --bitscount 50 --username "50cent" i'm a punk bitch and i want hh`)
	<-time.After(2 * time.Second)
	pm := twitch.PrivateMessage{
		User:    twitch.User{Name: "aerionblue"},
		Type:    twitch.PRIVMSG,
		Channel: "testing",
		Message: "!bid put it all on RAW DANGER",
	}
	b.dispatchBidCommand(pm)
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	configPath := flag.String("config_json", "", "Path to the bot config JSON file. Required.")
	twitchChatCredsPath := flag.String("twitch_chat_creds", "", "Path to the Twitch chat credentials file")
	twitchChatRepliesEnabled := flag.Bool("chat_replies_enabled", true, "Whether Twitch chat replies are enabled")
	firestoreCredsPath := flag.String("firestore_creds", "", "Path to the Firestore credentials file")
	sheetsCredsPath := flag.String("sheets_creds", "", "Path to the Google Sheets OAuth client secret file")
	sheetsTokenPath := flag.String("sheets_token", "", "Path to the Google Sheets OAuth token. If absent, you will be prompted to create a new token")
	streamelementsCredsPath := flag.String("streamelements_creds", "", "Path to a StreamElements config file. If absent, StreamElements donation checking will be disabled")
	streamlabsCredsPath := flag.String("streamlabs_creds", "", "Path to a Streamlabs OAuth token. If absent, Streamlabs donation checking will be disabled")
	tipLogPath := flag.String("tip_log_path", "", "Path to a text file where some other process is logging incoming donations")
	bidWarDataPath := flag.String("bidwar_data", "", "Path to a JSON file describing the current bid wars")
	flag.Parse()

	if *configPath == "" {
		log.Fatalf("--config_json flag is required")
	}
	cfg, err := ParseBotConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}

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
	var seDonationPoller *streamelements.DonationPoller
	var slDonationPoller *streamlabs.DonationPoller
	var tipWatcher *tipfile.Watcher
	var bidwarTallier *bidwar.Tallier
	if *sheetsCredsPath != "" {
		var err error
		sheetsSrv, err := googlesheets.NewService(context.Background(), *sheetsCredsPath, *sheetsTokenPath)
		if err != nil {
			log.Fatalf("error initializing Google Sheets API: %v", err)
		}
		donationTable := googlesheets.NewDonationTable(sheetsSrv, cfg.Spreadsheet.ID, cfg.Spreadsheet.SheetName)
		dbRecorder = db.NewGoogleSheetsClient(donationTable)
		bidwarTallier = bidwar.NewTallier(sheetsSrv, donationTable, cfg.Spreadsheet.ID, bidwars)
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
	if *streamelementsCredsPath != "" {
		var err error
		seDonationPoller, err = streamelements.NewDonationPoller(context.Background(), *streamelementsCredsPath, *targetChannel)
		if err != nil {
			log.Printf("(non-fatal) error initializing StreamElements polling: %v", err)
		}
	} else {
		log.Print("no StreamElements token provided")
	}
	if *streamlabsCredsPath != "" {
		var err error
		slDonationPoller, err = streamlabs.NewDonationPoller(context.Background(), *streamlabsCredsPath, *targetChannel)
		if err != nil {
			log.Printf("(non-fatal) error initializing Streamlabs polling: %v", err)
		}
	} else {
		log.Print("no Streamlabs token provided")
	}
	if *tipLogPath != "" {
		tipWatcher, err = tipfile.NewWatcher(*tipLogPath, *targetChannel)
		if err != nil {
			log.Fatalf("error creating tip file watcher: %v", err)
		}
		defer tipWatcher.Close()
	}

	b := &bot{
		ircClient:         ircClient,
		ircRepliesEnabled: ircRepliesEnabled,
		dbRecorder:        dbRecorder,
		bidwars:           bidwars,
		bidwarTallier:     bidwarTallier,
		minimumDonation:   minimumDonation,
		chatLimiter:       rate.NewLimiter(rate.Every(chatCooldown), chatBucketSize),
		communityGifts:    make(map[string]time.Time),
		pendingBids:       make(map[string]*bidPreference),
	}

	ircClient.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		if ev, ok := donation.ParseSubEvent(m); ok {
			b.dispatchSubEvent(ev)
		}
	})
	ircClient.OnPrivateMessage(func(m twitch.PrivateMessage) {
		if ev, ok := donation.ParseBitsEvent(m); ok {
			b.dispatchBitsEvent(ev)
		} else if firstTokenIs(strings.ToLower(m.Message), bidCommand) {
			b.dispatchBidCommand(m)
		}
	})
	ircClient.Join(*targetChannel)

	if seDonationPoller != nil {
		seDonationPoller.OnDonation(func(ev donation.Event) {
			b.dispatchMoneyDonation(ev)
		})
		if err := seDonationPoller.Start(); err != nil {
			log.Fatalf("StreamElements polling error: %v", err)
		}
	}
	if slDonationPoller != nil {
		slDonationPoller.OnDonation(func(ev donation.Event) {
			b.dispatchMoneyDonation(ev)
		})
		if err := slDonationPoller.Start(); err != nil {
			log.Fatalf("Streamlabs polling error: %v", err)
		}
	}

	if tipWatcher != nil {
		go func() {
			for {
				select {
				case ev := <-tipWatcher.C:
					b.dispatchMoneyDonation(ev)
				}
			}
		}()
	}

	if !*prod {
		go doLocalTest(b, *targetChannel, ircClient, bidwarTallier)
	}

	log.Print("connecting to IRC...")
	if err := ircClient.Connect(); err != nil {
		panic(err)
	}
}
