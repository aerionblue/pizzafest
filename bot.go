package main

import (
	"flag"
	"log"
	"strconv"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v2"
)

const subDollarValue = 5

const testIRCAddress = "irc.fdgt.dev:6667"

// USERNOTICE message param tag names. See https://dev.twitch.tv/docs/irc/tags for param descriptions.
const (
	msgParamSubPlan           string = "msg-param-sub-plan"
	msgParamRecipientUserName        = "msg-param-recipient-user-name"
	msgParamMassGiftCount            = "msg-param-mass-gift-count"
	// fdgt sends "msg-params-gift-months" (plural params). Not sure whether that's accurate...
	msgParamGiftMonths = "msg-param-gift-months"
)

// Legal values for the msgParamSubPlan param.
const (
	subPlanPrime string = "Prime"
	subPlanTier1        = "1000"
	subPlanTier2        = "2000"
	subPlanTier3        = "3000"
)

type bot struct {
	client *twitch.Client
}

type subEventType int

const (
	unknownType subEventType = iota
	sub
	subGift
	massSubGift
)

func (b *bot) dispatchUserNoticeMessage(m twitch.UserNoticeMessage) {
	eventType := toEventType(m.MsgID)
	switch eventType {
	case sub, subGift, massSubGift:
		b.handleSubscriptionEvent(m, eventType)
	}
}

func (b *bot) handleSubscriptionEvent(m twitch.UserNoticeMessage, eventType subEventType) {
	ev := subEvent{Type: eventType, Owner: m.User.Name, Count: 1, DurationMonths: 1}
	for name, value := range m.MsgParams {
		switch name {
		case msgParamSubPlan:
			ev.Tier = value
		case msgParamGiftMonths:
			n, err := strconv.Atoi(value)
			if err != nil {
				log.Printf("unexpected value for %s param: %v", msgParamGiftMonths, err)
				n = 1
			}
			ev.DurationMonths = n
		case msgParamMassGiftCount:
			n, err := strconv.Atoi(value)
			if err != nil {
				log.Printf("unexpected value for %s param: %v", msgParamMassGiftCount, err)
				n = 1
			}
			ev.Count = n
		}
	}
	// TODO(aerion): Batch up multiple sub gifts. Maybe the answer here is to catch the community sub event and then ignore all gift sub events for a certain period of time.
	log.Printf("new subscription by %v worth %d dollars (tier: %s, months: %d, count: %d)", ev.Owner, ev.DollarValue(), ev.Tier, ev.DurationMonths, ev.Count)
}

type subEvent struct {
	Type subEventType
	// Twitch username of the user who gets credit for this sub.
	Owner string
	// The number of subscriptions given at once.
	Count int
	// The subscription tier. TODO(aerion): Define a type for this.
	Tier string
	// How many months were purchased at once. Used for multi-month gifts. Equal to 1 for non-gifted subs.
	DurationMonths int
}

// DollarValue returns the dollar value this event should contribute to a bid war.
func (s subEvent) DollarValue() int {
	tierMultiplier := 1
	switch s.Tier {
	case subPlanPrime, subPlanTier1:
		tierMultiplier = 1
	case subPlanTier2:
		tierMultiplier = 2
	case subPlanTier3:
		tierMultiplier = 6
	}
	return subDollarValue * tierMultiplier * s.DurationMonths * s.Count
}

// eventType interprets the msg-id tag of a USERNOTICE message. Not all valid values are listed here; see the docs for a comprehensive list.
func toEventType(msgID string) subEventType {
	switch msgID {
	case "sub", "resub":
		return sub
	case "subgift":
		return subGift
	case "submysterygift":
		return massSubGift
	}
	// TODO(aerion): Maybe handle "giftpaidupgrade", "anongiftpaidupgrade" if they actually happen.
	return unknownType
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
