// Package grantconnect reads Grant Award List exports from GrantConnect
// (grants.gov.au), which has no API. The site exports at most 50,000 awards
// per search, and its export can repeat a 10,000-row block in place of the
// next one, so exports are made over date ranges of 10,000 awards or fewer
// (see README). ReadFile reports both problems; Merge counts each award once.
package grantconnect

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"spendingexplorer/internal/austender"
)

// RecipientKey identifies a recipient by name, as GrantConnect publishes no
// ABN: the supplier name key, so spellings that differ only in case,
// punctuation or a company suffix are one recipient, and a recipient can be
// matched to a supplier of the same name.
func RecipientKey(name string) string {
	return strings.TrimPrefix(austender.SupplierKey(name, ""), "name:")
}

// RecipientSlug is a recipient key as a URL path segment. Keys hold only
// letters, digits and single spaces, so KeyFromSlug reverses it.
func RecipientSlug(key string) string { return strings.ReplaceAll(key, " ", "-") }

func KeyFromSlug(slug string) string { return strings.ReplaceAll(slug, "-", " ") }

// Award is one grant award as published. Value is the award's current total
// in whole dollars, over its whole term; the export lists only the latest
// version of an amended award.
type Award struct {
	ID              string // as published, e.g. GA270901-V1
	Base            string // without the version, e.g. GA270901
	Version         int    // 0 for an award never varied
	Activity        string
	Agency          string
	PublishDate     string // YYYY-MM-DD
	Category        string
	StartDate       string
	EndDate         string
	Value           int64
	GOID            string // grant opportunity, when there was one
	Recipient       string
	OneOff          bool
	Aggregate       bool // several awards to one recipient reported as one
	AggregateReason string
	Aggregated      int
}

// File is one export.
type File struct {
	Range    string // the date range searched, as the export states it
	Capped   bool   // the search returned more than 50,000 awards; the file is incomplete
	Repeated int    // rows that repeat an award already in the file
	Awards   []Award
}

// ReadFile reads a Grant Award List export (.xlsx).
func ReadFile(path string) (*File, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetList()[0], excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	return parseRows(rows)
}

var idPattern = regexp.MustCompile(`^(GA\d+)(?:-V(\d+))?$`)

// parseRows reads the criteria summary above the table, then the table.
func parseRows(rows [][]string) (*File, error) {
	out := &File{}
	header := -1
	for i, row := range rows {
		if len(row) == 0 {
			continue
		}
		first := strings.TrimSpace(row[0])
		switch {
		case first == "Date Range" && len(row) > 1:
			out.Range = strings.TrimSpace(row[1])
		case strings.Contains(first, "more than 50000 results"):
			out.Capped = true
		case first == "GA ID" && len(row) > 2:
			header = i
		}
		if header >= 0 {
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("no GA ID header row")
	}
	col := map[string]int{}
	for i, h := range rows[header] {
		col[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{"GA ID", "Grant Activity", "Agency", "Publish Date", "Category", "Start Date", "End Date", "Value (AUD)", "Recipient Name"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("no %q column", need)
		}
	}
	get := func(row []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	seen := map[string]bool{}
	for n, row := range rows[header+1:] {
		id := get(row, "GA ID")
		if id == "" {
			continue
		}
		m := idPattern.FindStringSubmatch(id)
		if m == nil {
			return nil, fmt.Errorf("row %d: unexpected GA ID %q", header+n+2, id)
		}
		if seen[id] {
			out.Repeated++
			continue
		}
		seen[id] = true
		a := Award{
			ID: id, Base: m[1], Activity: get(row, "Grant Activity"), Agency: get(row, "Agency"),
			Category: get(row, "Category"), GOID: get(row, "GO ID"), Recipient: get(row, "Recipient Name"),
			OneOff:          strings.EqualFold(get(row, "One-off/Ad hoc"), "Yes"),
			Aggregate:       strings.EqualFold(get(row, "Aggregate Grant Award"), "Yes"),
			AggregateReason: get(row, "Aggregate Reason"),
		}
		a.Version, _ = strconv.Atoi(m[2])
		a.Aggregated, _ = strconv.Atoi(get(row, "Number of Awards Aggregated"))
		var err error
		if a.PublishDate, err = excelDate(get(row, "Publish Date")); err != nil {
			return nil, fmt.Errorf("%s publish date: %w", id, err)
		}
		if a.StartDate, err = excelDate(get(row, "Start Date")); err != nil {
			return nil, fmt.Errorf("%s start date: %w", id, err)
		}
		if a.EndDate, err = excelDate(get(row, "End Date")); err != nil {
			return nil, fmt.Errorf("%s end date: %w", id, err)
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(get(row, "Value (AUD)"), ",", ""), 64)
		if err != nil {
			return nil, fmt.Errorf("%s value: %w", id, err)
		}
		a.Value = int64(math.Round(v))
		out.Awards = append(out.Awards, a)
	}
	return out, nil
}

// Merge combines exports, skipping capped ones, and keeps one record per
// award: the highest version seen.
func Merge(files []*File) []Award {
	best := map[string]Award{}
	var order []string
	for _, f := range files {
		if f.Capped {
			continue
		}
		for _, a := range f.Awards {
			prev, ok := best[a.Base]
			if !ok {
				order = append(order, a.Base)
			}
			if !ok || a.Version > prev.Version {
				best[a.Base] = a
			}
		}
	}
	out := make([]Award, len(order))
	for i, b := range order {
		out[i] = best[b]
	}
	return out
}

var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// excelDate converts an Excel serial date to YYYY-MM-DD; an empty cell is "".
func excelDate(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "", err
	}
	return excelEpoch.AddDate(0, 0, int(math.Floor(f))).Format("2006-01-02"), nil
}
