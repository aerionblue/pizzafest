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

type SubTier int

const (
	unknownTier SubTier = 0
	SubTier1            = 1
	SubTier2            = 2
	SubTier3            = 3
)

func (s SubTier) Marshal() int {
	return int(s)
}

// UnmarshalSubTier converts an int to a SubTier.
func UnmarshalSubTier(n int) SubTier {
	switch n {
	case 1:
		return SubTier1
	case 2:
		return SubTier2
	case 3:
		return SubTier3
	}
	return unknownTier
}

// parseSubTier converts the sub tier parameter from a Twitch IRC message to a SubTier.
func parseSubTier(s string) SubTier {
	switch s {
	case subPlanPrime, subPlanTier1:
		return SubTier1
	case subPlanTier2:
		return SubTier2
	case subPlanTier3:
		return SubTier3
	}
	return unknownTier
}

type Event struct {
	// Twitch username of the user who gets credit for this sub.
	Owner string
	// The number of subscriptions. Equal to 1 for regular subs and resubs. Can be more than 1 when multiple subs are gifted at once.
	SubCount int
	// The subscription tier.
	SubTier SubTier
	// How many months were purchased at once. Used for multi-month gifts. Equal to 1 for non-gifted subs.
	SubMonths int
	// The number of bits donated.
	Bits int
	// The chat message included with the event.
	Message string
}

// DollarValue returns the dollar value this event should contribute to a bid war.
func (e Event) DollarValue() int {
	tierMultiplier := 1
	switch e.SubTier {
	case SubTier1:
		tierMultiplier = 1
	case SubTier2:
		tierMultiplier = 2
	case SubTier3:
		tierMultiplier = 6
	}
	return subDollarValue*tierMultiplier*e.SubMonths*e.SubCount + e.Bits/100
}

// ParseSubEvent parses a USERNOTICE message into an Event. Returns (Event{}, false) if the message does not represent a subscription.
func ParseSubEvent(m twitch.UserNoticeMessage) (Event, bool) {
	eventType := toSubEventType(m.MsgID)
	if eventType != sub {
		return Event{}, false
	}

	ev := Event{Owner: m.User.Name, SubCount: 1, SubMonths: 1, Message: m.Message}
	for name, value := range m.MsgParams {
		switch name {
		case msgParamSubPlan:
			ev.SubTier = parseSubTier(value)
		case msgParamGiftMonths:
			n, err := strconv.Atoi(value)
			if err != nil {
				log.Printf("unexpected value for %s param: %v", msgParamGiftMonths, err)
				n = 1
			}
			ev.SubMonths = n
		case msgParamMassGiftCount:
			n, err := strconv.Atoi(value)
			if err != nil {
				log.Printf("unexpected value for %s param: %v", msgParamMassGiftCount, err)
				n = 1
			}
			ev.SubCount = n
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

func ParseBitsEvent(m twitch.PrivateMessage) (Event, bool) {
	if m.Bits <= 0 {
		return Event{}, false
	}
	return Event{Owner: m.User.Name, Bits: m.Bits, Message: m.Message}, true
}
