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

type bot struct {
	ircClient *twitch.Client
	dbClient  *db.Client
}

func (b *bot) dispatchUserNoticeMessage(m twitch.UserNoticeMessage) {
	ev, ok := donation.ParseSubEvent(m)
	if !ok {
		return
	}
	// TODO(aerion): Batch up multiple sub gifts. Maybe the answer here is to catch the community sub event and then ignore all gift sub events for a certain period of time.
	log.Printf("new subscription by %v worth %d dollars (tier: %s, months: %d, count: %d)", ev.Owner, ev.DollarValue(), ev.SubTier, ev.SubMonths, ev.SubCount)
	if err := b.dbClient.RecordDonation(ev); err != nil {
		log.Printf("ERROR writing sub to db: %v", err)
	}
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	firestoreCredsPath := flag.String("firestore_creds", "", "Path to the Firestore credentials file")
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

	dbClient, err := db.NewClient(context.Background(), *firestoreCredsPath)
	if err != nil {
		log.Fatal("error connecting to db: %v", err)
	}

	b := &bot{ircClient, dbClient}

	ircClient.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		b.dispatchUserNoticeMessage(m)
	})
	ircClient.Join(*targetChannel)

	if !*prod {
		go func() {
			<-time.After(2 * time.Second)
			ircClient.Say(*targetChannel, "subgift --tier 2 --months 6 --username usedpizza --username2 AEWC20XX")
		}()
	}

	log.Print("connecting... ")
	if err := ircClient.Connect(); err != nil {
		panic(err)
	}
}
