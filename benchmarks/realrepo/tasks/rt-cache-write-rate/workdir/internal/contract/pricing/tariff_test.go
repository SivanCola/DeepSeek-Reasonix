package pricing

import (
	"testing"
	"time"
)

func beijingTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", s, time.FixedZone("", beijing))
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// The next flip is the next whole hour of the vendor's schedule where the answer
// changes, in the vendor's own zone and across a weekend.
func TestNextChangeFollowsTheVendorsScheduleAcrossAWeekend(t *testing.T) {
	for _, tc := range []struct {
		now, want string
		peak      bool
	}{
		{"2026-10-12 08:30", "2026-10-12 09:00", false}, // Monday before the morning peak
		{"2026-10-12 11:59", "2026-10-12 12:00", true},  // the morning peak ends
		{"2026-10-12 12:00", "2026-10-12 14:00", false}, // lunch
		{"2026-10-12 17:30", "2026-10-12 18:00", true},  // the afternoon peak ends
		{"2026-10-16 18:00", "2026-10-19 09:00", false}, // Friday evening to Monday morning
		{"2026-10-17 10:00", "2026-10-19 09:00", false}, // a Saturday is off-peak all day
	} {
		now := beijingTime(t, tc.now)
		if got := deepseekPeak.IsPeak(now); got != tc.peak {
			t.Errorf("IsPeak(%s) = %v, want %v", tc.now, got, tc.peak)
		}
		got, ok := deepseekPeak.NextChange(now)
		if !ok || !got.Equal(beijingTime(t, tc.want)) {
			t.Errorf("NextChange(%s) = %v %v, want %s", tc.now, got, ok, tc.want)
		}
	}
}

func TestNextChangeOfAWindowThatNeverFlipsIsNone(t *testing.T) {
	var w *PeakWindow
	if _, ok := w.NextChange(time.Now()); ok {
		t.Fatal("a nil window flips")
	}
	if _, ok := (&PeakWindow{OffsetSeconds: beijing}).NextChange(time.Now()); ok {
		t.Fatal("a window with no hours flips")
	}
}

// Only a rate confirmed as the vendor's own earns a tariff, and only where the
// vendor's table has a peak rate.
func TestLiveTariffIsTheVendorsAndNeverTheUsers(t *testing.T) {
	official := *CurrentRate("deepseek", "deepseek-flash", "CNY")
	at := beijingTime(t, "2026-10-12 10:00")
	tariff, ok := LiveTariff("deepseek", "deepseek-flash", official, at)
	if !ok || !tariff.Peak || !tariff.Changes || !tariff.ChangesAt.Equal(beijingTime(t, "2026-10-12 12:00")) {
		t.Fatalf("official rate at a peak hour = %+v %v, want peak until 12:00", tariff, ok)
	}
	custom := official
	custom.Input = official.Input * 3
	if _, ok := LiveTariff("deepseek", "deepseek-flash", custom, at); ok {
		t.Fatal("a custom price was given the vendor's schedule")
	}
	if _, ok := LiveTariff("", "deepseek-flash", official, at); ok {
		t.Fatal("a relay with no vendor kind was given the vendor's schedule")
	}
}
