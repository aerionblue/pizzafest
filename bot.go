package main

import (
	"flag"
	"log"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v2"

	"github.com/aerionblue/pizzafest/donation"
)

const testIRCAddress = "irc.fdgt.dev:6667"

type bot struct {
	client *twitch.Client
}

func (b *bot) dispatchUserNoticeMessage(m twitch.UserNoticeMessage) {
	ev, ok := donation.ParseSubEvent(m)
	if !ok {
		return
	}
	// TODO(aerion): Batch up multiple sub gifts. Maybe the answer here is to catch the community sub event and then ignore all gift sub events for a certain period of time.
	log.Printf("new subscription by %v worth %d dollars (tier: %s, months: %d, count: %d)", ev.Owner, ev.DollarValue(), ev.SubTier, ev.SubMonths, ev.SubCount)
}

func main() {
	prod := flag.Bool("prod", false, "Whether to use real twitch.tv IRC. If false, connects to fdgt instead.")
	targetChannel := flag.String("channel", "aerionblue", "The IRC channel to listen to")
	flag.Parse()

	client := twitch.NewAnonymousClient()
	client.Capabilities = []string{twitch.CommandsCapability, twitch.TagsCapability}
	if *prod {
		log.Printf("*** CONNECTING TO PROD #%s ***", *targetChannel)
	} else {
		log.Printf("--- connecting to fdgt #%s ---", *targetChannel)
		client.IrcAddress = testIRCAddress
		client.TLS = false
	}

	b := &bot{client: client}

	client.OnUserNoticeMessage(func(m twitch.UserNoticeMessage) {
		b.dispatchUserNoticeMessage(m)
	})
	client.Join(*targetChannel)

	if !*prod {
		go func() {
			<-time.After(2 * time.Second)
			client.Say(*targetChannel, "subgift --tier 2 --months 6 --username usedpizza --username2 AEWC20XX")
		}()
	}

	log.Print("connecting... ")
	if err := client.Connect(); err != nil {
		panic(err)
	}
}
