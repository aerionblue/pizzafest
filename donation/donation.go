package donation

import (
	"log"
	"strconv"

	twitch "github.com/gempir/go-twitch-irc/v2"
)

const subDollarValue = 5

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

type EventType int

const (
	unknown EventType = iota
	sub
)

type Event struct {
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
func (e Event) DollarValue() int {
	tierMultiplier := 1
	switch e.Tier {
	case subPlanPrime, subPlanTier1:
		tierMultiplier = 1
	case subPlanTier2:
		tierMultiplier = 2
	case subPlanTier3:
		tierMultiplier = 6
	}
	return subDollarValue * tierMultiplier * e.DurationMonths * e.Count
}

// ParseSubEvent parses a USERNOTICE message into an Event. Returns (Event{}, false) if the message does not represent a subscription.
func ParseSubEvent(m twitch.UserNoticeMessage) (Event, bool) {
	eventType := toSubEventType(m.MsgID)
	if eventType != sub {
		return Event{}, false
	}

	ev := Event{Owner: m.User.Name, Count: 1, DurationMonths: 1}
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
	return ev, true
}

// eventType interprets the msg-id tag of a USERNOTICE message. Not all valid values are listed here; see the docs for a comprehensive list.
func toSubEventType(msgID string) EventType {
	switch msgID {
	case "sub", "resub", "subgift", "submysterygift":
		return sub
	}
	// TODO(aerion): Maybe handle "giftpaidupgrade", "anongiftpaidupgrade" if they actually happen.
	return unknown
}
