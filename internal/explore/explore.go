// Package explore holds the view model shared by the web pages: nodes in the
// spending hierarchy, money formatting and the treemap layout.
package explore

import (
	"fmt"
	"math"
	"regexp"
)

// Node is one box in the hierarchy (portfolio, entity or program).
type Node struct {
	Name     string
	URL      string
	Cur      int64 // current fiscal year, whole dollars
	Prev     int64 // previous fiscal year from the same publication
	Excluded int64 // removed as double counting or non-expense, current year
	Itemised int64 // annualised contract and grant value traced to this node (estimate)
	Funded   int64 // corporate funding from other entities, counted where it is paid from and not in Cur
	Children int
	Note     string // short description shown under the name, e.g. a program objective
}

// InAll is what the node spends, including funding counted where it is paid from.
func (n Node) InAll() int64 { return n.Cur + n.Funded }

// Change is the year-on-year change in dollars.
func (n Node) Change() int64 { return n.Cur - n.Prev }

// ChangePct is the year-on-year change in per cent; ok is false when there is
// no previous-year amount to compare with.
func (n Node) ChangePct() (pct float64, ok bool) {
	if n.Prev <= 0 {
		return 0, false
	}
	return 100 * float64(n.Cur-n.Prev) / float64(n.Prev), true
}

// Money formats whole dollars compactly: $833.3bn, $512m, $45k.
func Money(d int64) string {
	neg := d < 0
	a := math.Abs(float64(d))
	var s string
	switch {
	case a >= 1e9:
		s = fmt.Sprintf("$%.1fbn", a/1e9)
	case a >= 1e6:
		s = fmt.Sprintf("$%.0fm", a/1e6)
		if a < 1e7 {
			s = fmt.Sprintf("$%.1fm", a/1e6)
		}
	case a >= 1e3:
		s = fmt.Sprintf("$%.0fk", a/1e3)
	default:
		s = fmt.Sprintf("$%.0f", a)
	}
	if neg {
		return "−" + s
	}
	return s
}

// SignedMoney is Money with an explicit + for increases.
func SignedMoney(d int64) string {
	if d > 0 {
		return "+" + Money(d)
	}
	if d == 0 {
		return "no change"
	}
	return Money(d)
}

// Pct formats a percentage with one decimal and an explicit sign.
func Pct(p float64) string {
	switch {
	case p > 0:
		return fmt.Sprintf("+%.1f%%", p)
	case p < 0:
		return fmt.Sprintf("−%.1f%%", -p)
	}
	return "0.0%"
}

// Share returns part as a percentage of whole.
func Share(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

var footnoteMarks = regexp.MustCompile(`\s*[(\[][a-z][)\]]`)

// DisplayName drops footnote markers such as "(b) (c)" that the PBS tables
// carry in program names; the published name is kept in the database.
func DisplayName(name string) string {
	return footnoteMarks.ReplaceAllString(name, "")
}

// PreviousYear returns the fiscal year before fy, e.g. 2026-27 -> 2025-26.
func PreviousYear(fy string) string {
	var a, b int
	fmt.Sscanf(fy, "%d-%d", &a, &b)
	return fmt.Sprintf("%d-%02d", a-1, (b+99)%100)
}
