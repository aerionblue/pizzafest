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

type SubEventType int

const (
	unknown SubEventType = iota
	Subscription
	GiftSubscription
	CommunityGift
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

func (s SubTier) multiplier() int {
	switch s {
	case SubTier1:
		return 1
	case SubTier2:
		return 2
	case SubTier3:
		return 6
	}
	return 0
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
	Type SubEventType
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
	// The number of US cents donated.
	Cents int
	// The chat message included with the event.
	Message string
}

// DollarValue returns the dollar value this event should contribute to a bid war.
// TODO(aerion): Express everything as cents instead of dollars.
func (e Event) DollarValue() int {
	return subDollarValue*e.SubValue() + e.Bits/100 + e.Cents/100
}

// SubValue returns this event's equivalent value in Tier 1 subscriptions.
func (e Event) SubValue() int {
	return e.SubTier.multiplier() * e.SubMonths * e.SubCount
}

// ParseSubEvent parses a USERNOTICE message into an Event. Returns (Event{}, false) if the message does not represent a subscription.
func ParseSubEvent(m twitch.UserNoticeMessage) (Event, bool) {
	eventType := toSubEventType(m.MsgID)
	if eventType == unknown {
		return Event{}, false
	}

	ev := Event{Type: eventType, Owner: m.User.Name, SubCount: 1, SubMonths: 1, Message: m.Message}
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
func toSubEventType(msgID string) SubEventType {
	switch msgID {
	case "sub", "resub":
		return Subscription
	case "subgift":
		return GiftSubscription
	case "submysterygift":
		return CommunityGift
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
