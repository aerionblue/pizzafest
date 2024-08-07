package donation

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	twitch "github.com/gempir/go-twitch-irc/v2"
)

// USERNOTICE message param tag names. See https://dev.twitch.tv/docs/irc/tags for param descriptions.
const (
	msgParamSubPlan           string = "msg-param-sub-plan"
	msgParamRecipientUserName        = "msg-param-recipient-user-name"
	msgParamMassGiftCount            = "msg-param-mass-gift-count"
	msgParamGiftMonths               = "msg-param-gift-months"
	// Whether a resub was part of a multi-month gift sub. The
	// msg-param-gift-month-being-redeemed parameter indicates how many
	// months of the original gift have passed (e.g., this is month 7 of a
	// 12-month gift).
	msgParamWasGifted              = "msg-param-was-gifted"
	msgParamGiftMonthBeingRedeemed = "msg-param-gift-month-being-redeemed"
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
	unknownTier  SubTier = 0
	SubTier1             = 1
	SubTier2             = 2
	SubTier3             = 3
	SubTierPrime         = 101
)

func (s SubTier) Marshal() int {
	return int(s)
}

func (s SubTier) description() string {
	switch s {
	case SubTier1:
		return "Tier 1"
	case SubTier2:
		return "Tier 2"
	case SubTier3:
		return "Tier 3"
	case SubTierPrime:
		return "Prime"
	}
	return "unknown"
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
	case 101:
		return SubTierPrime
	}
	return unknownTier
}

// parseSubTier converts the sub tier parameter from a Twitch IRC message to a SubTier.
func parseSubTier(s string) SubTier {
	switch s {
	case subPlanPrime:
		return SubTierPrime
	case subPlanTier1:
		return SubTier1
	case subPlanTier2:
		return SubTier2
	case subPlanTier3:
		return SubTier3
	}
	return unknownTier
}

type Event struct {
	// Twitch username of the user who gets credit for this donation.
	Owner string
	// Twitch channel to which this donation was given.
	Channel string
	// The type of subscription (if this event is a sub event).
	Type SubEventType
	// The number of subscriptions. Equal to 1 for regular subs and resubs. Can
	// be more than 1 when multiple subs are gifted at once.
	SubCount int
	// The subscription tier.
	SubTier SubTier
	// How many months were purchased at once. Used for multi-month gifts. Equal
	// to 1 for non-gifted subs.
	SubMonths int
	// The number of bits donated.
	Bits int
	// The number of US cents donated.
	Cash CentsValue
	// The chat message included with the event.
	Message string
}

// CentsValue returns the value that this event should contribute to a bid war,
// in US cents.
func (e Event) Value() CentsValue {
	return CentsValue(e.SubCentsValue() + e.Bits + e.Cash.Cents())
}

// SubCentsValue returns this event's equivalent value in cents.
func (e Event) SubCentsValue() int {
	baseValue := 0
	switch e.SubTier {
	case SubTierPrime:
		baseValue = 500
	case SubTier1:
		baseValue = 600
	case SubTier2:
		baseValue = 1200
	case SubTier3:
		baseValue = 2500
	}
	return baseValue * e.SubMonths * e.SubCount
}

// Description returns a human-readable description of the event.
func (e Event) Description() string {
	// In practice, it's not possible for more than one of bits/dollars/subs
	// to occur in the same Event, but we still handle it.
	var parts []string
	if e.Cash.Cents() > 0 {
		parts = append(parts, fmt.Sprintf("$%s donation", e.Cash))
	}
	if e.Bits > 0 {
		parts = append(parts, fmt.Sprintf("%d bits", e.Bits))
	}
	if e.SubCount > 0 {
		var subParts []string
		if e.SubCount > 1 {
			subParts = append(subParts, fmt.Sprintf("%dx", e.SubCount))
		}
		if e.SubTier != SubTier1 {
			subParts = append(subParts, e.SubTier.description())
		}
		switch e.Type {
		case Subscription:
			subParts = append(subParts, "sub")
		case GiftSubscription, CommunityGift:
			subParts = append(subParts, "gift sub")
		}
		parts = append(parts, strings.Join(subParts, " "))
	}
	return strings.Join(parts, " + ")
}

// ParseSubEvent parses a USERNOTICE message into an Event. Returns (Event{}, false) if the message does not represent a subscription.
func ParseSubEvent(m twitch.UserNoticeMessage) (Event, bool) {
	eventType := toSubEventType(m.MsgID)
	if eventType == unknown {
		return Event{}, false
	}

	ev := Event{
		Owner: m.User.Name, Channel: m.Channel,
		Type: eventType, SubCount: 1, SubMonths: 1,
		Message: m.Message,
	}
	wasGifted := false
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
		case msgParamWasGifted:
			fallthrough
		case msgParamGiftMonthBeingRedeemed:
			// I'm not sure when exactly either of these parameters is set on
			// gifted resubs. It doesn't happen that often, so it's kind of
			// annoying to verify. Let's just look for both.
			wasGifted = true
		case msgParamMassGiftCount:
			n, err := strconv.Atoi(value)
			if err != nil {
				log.Printf("unexpected value for %s param: %v", msgParamMassGiftCount, err)
				n = 1
			}
			ev.SubCount = n
		}
	}
	if wasGifted {
		// If a user receives an N-month gift, they can send N resub messages, all of which
		// carry the original gift month count. Each event should only count for 1 month.
		ev.SubMonths = 1
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
	return Event{Owner: m.User.Name, Channel: m.Channel, Bits: m.Bits, Message: m.Message}, true
}

// Value is the value of a donation.
type CentsValue int

// String expresses the value in points, with 2 decimal places.
func (v CentsValue) String() string {
	return fmt.Sprintf("%0.2f", v.Points())
}

// Points converts the value to Pizza Fest points (one point per dollar).
func (v CentsValue) Points() float64 {
	return float64(v) / 100
}

func (v CentsValue) Cents() int {
	return int(v)
}
