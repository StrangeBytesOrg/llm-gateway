package gateway

import (
	"errors"
	"time"
)

// Period derives every boundary from the original UTC anchor, avoiding month-end drift.
func Period(anchor, now time.Time, cadence string) (time.Time, time.Time, error) {
	anchor = anchor.UTC()
	now = now.UTC()
	if now.Before(anchor) {
		return time.Time{}, time.Time{}, errors.New("budget has not started")
	}
	switch cadence {
	case "weekly":
		n := (now.Unix() - anchor.Unix()) / 604800
		start := time.Unix(anchor.Unix()+n*604800, int64(anchor.Nanosecond())).UTC()
		if start.After(now) {
			start = start.AddDate(0, 0, -7)
		}
		return start, start.AddDate(0, 0, 7), nil
	case "monthly":
		n := (now.Year()-anchor.Year())*12 + int(now.Month()-anchor.Month())
		start := monthBoundary(anchor, n)
		if start.After(now) {
			n--
			start = monthBoundary(anchor, n)
		}
		return start, monthBoundary(anchor, n+1), nil
	default:
		return time.Time{}, time.Time{}, errors.New("cadence must be weekly or monthly")
	}
}

func monthBoundary(a time.Time, n int) time.Time {
	first := time.Date(a.Year(), a.Month()+time.Month(n), 1, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), time.UTC)
	day := a.Day()
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), time.UTC)
}
