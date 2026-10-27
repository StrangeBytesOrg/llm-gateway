package gateway

import (
	"testing"
	"time"
)

func stamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func TestPeriod(t *testing.T) {
	for _, tc := range []struct{ name, cadence, anchor, now, start, end string }{
		{"weekly before boundary", "weekly", "2025-01-08T12:30:00Z", "2025-01-15T12:29:59Z", "2025-01-08T12:30:00Z", "2025-01-15T12:30:00Z"},
		{"weekly exact boundary", "weekly", "2025-01-08T12:30:00Z", "2025-01-15T12:30:00Z", "2025-01-15T12:30:00Z", "2025-01-22T12:30:00Z"},
		{"month end", "monthly", "2025-01-31T08:00:00Z", "2025-02-28T08:00:00Z", "2025-02-28T08:00:00Z", "2025-03-31T08:00:00Z"},
		{"no monthly drift", "monthly", "2025-01-31T08:00:00Z", "2025-03-30T08:00:00Z", "2025-02-28T08:00:00Z", "2025-03-31T08:00:00Z"},
		{"leap year", "monthly", "2024-01-31T08:00:00Z", "2024-02-29T08:00:00Z", "2024-02-29T08:00:00Z", "2024-03-31T08:00:00Z"},
		{"leap anniversary", "monthly", "2024-02-29T08:00:00Z", "2025-02-28T08:00:00Z", "2025-02-28T08:00:00Z", "2025-03-29T08:00:00Z"},
		{"year rollover", "monthly", "2024-12-15T00:00:00Z", "2025-01-14T00:00:00Z", "2024-12-15T00:00:00Z", "2025-01-15T00:00:00Z"},
		{"UTC conversion", "monthly", "2025-01-15T00:00:00-08:00", "2025-02-15T07:59:59Z", "2025-01-15T08:00:00Z", "2025-02-15T08:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, err := Period(stamp(tc.anchor), stamp(tc.now), tc.cadence)
			if err != nil || !a.Equal(stamp(tc.start)) || !b.Equal(stamp(tc.end)) {
				t.Fatalf("got %s %s %v", a, b, err)
			}
		})
	}
	if _, _, err := Period(stamp("2025-02-01T00:00:00Z"), stamp("2025-01-01T00:00:00Z"), "weekly"); err == nil {
		t.Fatal("future anchor accepted")
	}
}

func TestMoney(t *testing.T) {
	for _, tc := range []struct {
		s string
		n int64
	}{{"0", 0}, {"0.00000000049", 0}, {"0.0000000005", 1}, {"1.2345678915", 1234567892}, {"1e-8", 10}, {"9223372036.854775807", 9223372036854775807}} {
		n, err := ParseUSD(tc.s)
		if err != nil || n != tc.n {
			t.Errorf("%s: got %d %v", tc.s, n, err)
		}
	}
	for _, s := range []string{"-1", "NaN", "1/2", "1e99999999", "1e31", "1e-31", "", " 1", "9223372036.854775808", "0x1p2"} {
		if _, err := ParseUSD(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func FuzzMoney(f *testing.F) {
	for _, s := range []string{"0", "1.25", "1e-9", "1e99999", "NaN"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := ParseUSD(s)
		if err == nil {
			if n < 0 {
				t.Fatal("negative money")
			}
			roundtrip, err := ParseUSD(FormatUSD(n))
			if err != nil || roundtrip != n {
				t.Fatal("roundtrip mismatch")
			}
		}
	})
}
