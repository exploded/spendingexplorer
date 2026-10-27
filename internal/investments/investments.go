// Package investments reads the table that reconciles the underlying cash
// balance to the headline cash balance, from `pdftotext -layout` output of
// Budget Paper No. 1 (Statement 3) or the Final Budget Outcome (Part 1).
// Its rows are the net cash flows from investments in financial assets for
// policy purposes: loans and equity the government provides, which are not
// expenses.
package investments

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Table is one publication's itemised net cash flows, in $m as published:
// negative is net cash paid out.
type Table struct {
	Ref   string // e.g. "Table 3.5"
	Rows  []Row
	Total Row
}

// Row is one item with one amount per column of the published table.
type Row struct {
	Item    string
	Amounts []int64
}

var (
	tableRe    = regexp.MustCompile(`^\s*(Table \d+\.\d+): (.*)`)
	headingRe  = regexp.MustCompile(`(?i)reconciliation of (?:general government sector )?underlying and headline`)
	yearRe     = regexp.MustCompile(`^\d{4}-\d{2}$`)
	numberRe   = regexp.MustCompile(`^-?(?:\d{1,3}(?:,\d{3})+|\d+)$`)
	footnoteRe = regexp.MustCompile(`\s*\([a-z]\)$`)
	dashRe     = regexp.MustCompile(`\s*[-–—]+\s*`)
	spaceRe    = regexp.MustCompile(`\s+`)
)

// Parse finds the reconciliation table and reads every item row with exactly
// cols amounts. A label that wraps onto following lines is joined. The rows
// must sum to the published total in every column, within rounding.
func Parse(text string, cols int) (Table, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\f", "\n"), "\n")
	var t Table
	start := heading(lines, &t)
	if start < 0 {
		return t, fmt.Errorf("reconciliation table not found")
	}
	inItems := false
	label := ""
	for _, l := range lines[start+1:] {
		l = strings.TrimSpace(l)
		if !inItems {
			if strings.Contains(l, "policy purposes(a)") {
				inItems = true
			}
			continue
		}
		if l == "" {
			continue
		}
		text, nums, err := split(l)
		if err != nil {
			return t, err
		}
		if len(nums) == 0 {
			label = join(label, text)
			continue
		}
		if len(nums) != cols {
			return t, fmt.Errorf("%q: %d amounts, want %d", l, len(nums), cols)
		}
		r := Row{Item: footnoteRe.ReplaceAllString(join(label, text), ""), Amounts: nums}
		label = ""
		if strings.HasPrefix(r.Item, "Total net cash flows") {
			t.Total = r
			break
		}
		t.Rows = append(t.Rows, r)
	}
	if t.Total.Amounts == nil || len(t.Rows) == 0 {
		return t, fmt.Errorf("%s: no items or no total row", t.Ref)
	}
	for c := 0; c < cols; c++ {
		var sum int64
		for _, r := range t.Rows {
			sum += r.Amounts[c]
		}
		// Each item is rounded to $1m, so the sum may drift from the total.
		if d := sum - t.Total.Amounts[c]; d > int64(len(t.Rows)/2) || -d > int64(len(t.Rows)/2) {
			return t, fmt.Errorf("%s column %d: items sum to %d, total is %d", t.Ref, c+1, sum, t.Total.Amounts[c])
		}
	}
	return t, nil
}

// Years returns the fiscal years in the table's column header: the first
// line after the heading that holds nothing but years.
func Years(text string) []string {
	lines := strings.Split(text, "\n")
	start := heading(lines, &Table{})
	if start < 0 {
		return nil
	}
	for _, l := range lines[start+1:] {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		ok := true
		for _, w := range f {
			ok = ok && yearRe.MatchString(w)
		}
		if ok {
			return f
		}
	}
	return nil
}

// heading finds the table's heading, which may wrap onto a second line, sets
// t's reference, and returns the heading's last line index, or -1.
func heading(lines []string, t *Table) int {
	for i, l := range lines {
		m := tableRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		h := strings.TrimSpace(m[2])
		if !headingRe.MatchString(h) && i+1 < len(lines) {
			h = join(h, strings.TrimSpace(lines[i+1]))
			i++
		}
		if headingRe.MatchString(h) {
			t.Ref = m[1]
			return i
		}
	}
	return -1
}

// split separates a line's label from its trailing amounts.
func split(l string) (string, []int64, error) {
	f := strings.Fields(l)
	i := len(f)
	for i > 0 && numberRe.MatchString(f[i-1]) {
		i--
	}
	var nums []int64
	for _, w := range f[i:] {
		v, err := strconv.ParseInt(strings.ReplaceAll(w, ",", ""), 10, 64)
		if err != nil {
			return "", nil, err
		}
		nums = append(nums, v)
	}
	return strings.Join(f[:i], " "), nums, nil
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

// Key normalises an item name so the same item matches across publications,
// which differ in case and dashes ("Snowy Hydro Limited Loan", "Indonesia --
// loan").
func Key(item string) string {
	k := strings.ToLower(footnoteRe.ReplaceAllString(item, ""))
	k = dashRe.ReplaceAllString(k, " - ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(k, " "))
}
