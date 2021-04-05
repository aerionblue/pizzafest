package bidwar

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Collection is a set of bid wars.
type Collection struct {
	Contests []Contest
}

// Contest is a single bid war between several options. The option that
// receives the most money will win this contest.
type Contest struct {
	Name    string
	Options []Option
}

// Option is a contestant in a bid war. Donors can allocate money to an option
// to help it win its bid war.
type Option struct {
	DisplayName string
	// All the aliases by which this choice is known. Matching any of these
	// aliases in a donation message designates the money to this choice.
	Aliases []alias
}

// FindOption determines whether the given donation message or chat message
// mentioned one of the bid war options in this Collection, and returns that
// Option. If no bid war option was found, returns the zero Option. If more
// than one Option matches, returns the match that occurs earliest (leftmost)
// in the message.
func (c Collection) FindOption(msg string) Option {
	minIndex := -1
	minOpt := Option{}
	for _, con := range c.Contests {
		for _, opt := range con.Options {
			for _, a := range opt.Aliases {
				if loc := a.FindStringIndex(msg); loc != nil {
					idx := loc[0]
					if minIndex > idx || minIndex < 0 {
						minIndex = idx
						minOpt = opt
					}
				}
			}
		}
	}
	return minOpt
}

type alias struct {
	*regexp.Regexp
}

func (a *alias) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	// (?i) = case-insensitive; \b = ASCII word boundary
	r, err := regexp.Compile(fmt.Sprintf(`(?i)\b%s\b`, s))
	if err != nil {
		return fmt.Errorf("alias %v not suitable for regexp: %v", s, err)
	}
	a.Regexp = r
	return nil
}

func Parse(rawJson []byte) (Collection, error) {
	var c Collection
	if err := json.Unmarshal(rawJson, &c); err != nil {
		return Collection{}, err
	}
	return c, nil
}
