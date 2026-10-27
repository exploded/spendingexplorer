// Package fiscal spreads whole-of-life contract values across Australian
// financial years (1 July to 30 June). The result is an estimate: AusTender
// publishes a contract's total value and period, not when payments are made.
package fiscal

import (
	"fmt"
	"math"
	"time"
)

// Share is the part of a value that falls in one financial year.
type Share struct {
	FiscalYear string
	Amount     int64
}

const day = 24 * time.Hour

// Apportion spreads value evenly by day from start to end inclusive
// (YYYY-MM-DD). An end before the start, or a missing end, puts the whole value
// in the start's year. Rounding is corrected in the last year so the shares
// always sum to value.
func Apportion(value int64, start, end string) ([]Share, error) {
	s, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, fmt.Errorf("start %q: %w", start, err)
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil || e.Before(s) {
		e = s
	}
	totalDays := e.Sub(s)/day + 1

	var out []Share
	var given int64
	for fyStart := yearStart(s); !fyStart.After(e); fyStart = fyStart.AddDate(1, 0, 0) {
		fyEnd := fyStart.AddDate(1, 0, -1)
		from, to := maxTime(s, fyStart), minTime(e, fyEnd)
		days := to.Sub(from)/day + 1
		amount := int64(math.Round(float64(value) * float64(days) / float64(totalDays)))
		out = append(out, Share{FiscalYear: Label(fyStart), Amount: amount})
		given += amount
	}
	out[len(out)-1].Amount += value - given
	return out, nil
}

// yearStart is 1 July of the financial year containing t.
func yearStart(t time.Time) time.Time {
	y := t.Year()
	if t.Month() < time.July {
		y--
	}
	return time.Date(y, time.July, 1, 0, 0, 0, 0, time.UTC)
}

// Label formats the financial year starting at fyStart, e.g. 2026-27.
func Label(fyStart time.Time) string {
	y := fyStart.Year()
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// Bounds returns the first and last day (YYYY-MM-DD) of a financial year label.
func Bounds(fy string) (first, last string) {
	var y int
	fmt.Sscanf(fy, "%d-", &y)
	return fmt.Sprintf("%d-07-01", y), fmt.Sprintf("%d-06-30", y+1)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
