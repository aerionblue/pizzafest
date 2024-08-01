package donation

import (
	"testing"
)

func TestValue(t *testing.T) {
	for _, tc := range []struct {
		ev   Event
		want CentsValue
	}{
		{Event{SubTier: SubTier1, SubCount: 1, SubMonths: 1}, 500},
		{Event{SubTier: SubTier1, SubCount: 5, SubMonths: 1}, 2500},
		{Event{SubTier: SubTier1, SubCount: 1, SubMonths: 6}, 3000},
		{Event{SubTier: SubTier1, SubCount: 5, SubMonths: 6}, 15000},
		{Event{SubTier: SubTier2, SubCount: 1, SubMonths: 1}, 1000},
		{Event{SubTier: SubTier2, SubCount: 1, SubMonths: 6}, 6000},
		{Event{SubTier: SubTier3, SubCount: 1, SubMonths: 1}, 2500},
		{Event{SubTier: SubTier3, SubCount: 12, SubMonths: 1}, 30000},
		{Event{Bits: 420}, 420},
		{Event{Cash: CentsValue(501)}, 501},
	} {
		if got := tc.ev.Value(); got != tc.want {
			t.Errorf("wrong value for %v; got %v, want %v", tc.ev, got, tc.want)
		}
	}
}
