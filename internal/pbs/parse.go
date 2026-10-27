// Package pbs reads the Portfolio Budget Statements "Program Expense Line Items"
// tables published on data.gov.au with each Budget. Earlier years ship as CSV in
// Windows-1252; 2026-27 ships as XLSX. Amounts in the source are $'000.
package pbs

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

// Value is one fiscal-year cell of a line item.
type Value struct {
	FiscalYear   string
	Dollars      int64
	NotPublished bool // source shows "nfp"
}

// Line is one row of the line-items table.
type Line struct {
	Portfolio   string
	Entity      string
	Outcome     string
	Program     string
	ExpenseType string // as published
	Kind        string // administered | departmental | corporate
	LineItem    string // the "Appropriation_type" column, which names the line
	SourceTable string
	Row         int // row number in the published file, header = 1
	Values      []Value
}

// File is a parsed line-items table.
type File struct {
	Years []string // fiscal-year columns in order
	Lines []Line
}

var yearCol = regexp.MustCompile(`^\d{4}-\d{2}$`)

// ReadFile parses a CSV or XLSX line-items file.
func ReadFile(path string) (*File, error) {
	var rows [][]string
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		rows, err = readCSV(path)
	case ".xlsx":
		rows, err = readXLSX(path)
	default:
		return nil, fmt.Errorf("unsupported file type %q", filepath.Ext(path))
	}
	if err != nil {
		return nil, err
	}
	return parseRows(rows)
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(charmap.Windows1252.NewDecoder().Reader(f))
	r.FieldsPerRecord = -1
	var rows [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, rec)
	}
}

func readXLSX(path string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.GetRows(f.GetSheetList()[0], excelize.Options{RawCellValue: true})
}

func parseRows(rows [][]string) (*File, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty file")
	}
	col := map[string]int{}
	var years []string
	var yearIdx []int
	for i, h := range rows[0] {
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		if yearCol.MatchString(h) {
			years = append(years, h)
			yearIdx = append(yearIdx, i)
			continue
		}
		col[strings.ToLower(h)] = i
	}
	need := map[string][]string{
		"portfolio":   {"portfolio"},
		"entity":      {"department/agency", "agency name"},
		"outcome":     {"outcome"},
		"program":     {"program"},
		"expense":     {"expense_type"},
		"line":        {"appropriation_type"},
		"sourcetable": {"source_table"},
	}
	idx := map[string]int{}
	for key, names := range need {
		found := false
		for _, n := range names {
			if i, ok := col[n]; ok {
				idx[key], found = i, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("missing column %v", names)
		}
	}
	if len(years) == 0 {
		return nil, fmt.Errorf("no fiscal-year columns")
	}

	out := &File{Years: years}
	for n, rec := range rows[1:] {
		get := func(key string) string {
			if i := idx[key]; i < len(rec) {
				return CleanText(rec[i])
			}
			return ""
		}
		if get("entity") == "" && get("program") == "" {
			continue // trailing blank rows
		}
		kind, err := Kind(get("expense"))
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", n+2, err)
		}
		l := Line{
			Portfolio:   CanonicalPortfolio(get("portfolio")),
			Entity:      get("entity"),
			Outcome:     get("outcome"),
			Program:     get("program"),
			ExpenseType: get("expense"),
			Kind:        kind,
			LineItem:    get("line"),
			SourceTable: get("sourcetable"),
			Row:         n + 2,
		}
		for j, i := range yearIdx {
			if i >= len(rec) {
				continue
			}
			v, ok, err := ParseAmount(rec[i])
			if err != nil {
				return nil, fmt.Errorf("row %d, %s: %w", n+2, years[j], err)
			}
			if !ok {
				continue
			}
			v.FiscalYear = years[j]
			l.Values = append(l.Values, v)
		}
		out.Lines = append(out.Lines, l)
	}
	return out, nil
}

// ParseAmount converts a $'000 cell to whole dollars. ok is false for blank cells.
func ParseAmount(s string) (v Value, ok bool, err error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	switch strings.ToLower(s) {
	case "":
		return v, false, nil
	case "nfp":
		return Value{NotPublished: true}, true, nil
	case "-", "–", "—", "..":
		return Value{}, true, nil
	}
	neg := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	if neg {
		s = s[1 : len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return v, false, fmt.Errorf("bad amount %q", s)
	}
	if neg {
		f = -f
	}
	return Value{Dollars: int64(math.Round(f * 1000))}, true, nil
}

// portfolioNames expands abbreviated portfolio labels used in some files.
var portfolioNames = map[string]string{
	"Infra, Transport, Regional Developt, Comms, Sports and Arts": "Infrastructure, Transport, Regional Development, Communications, Sport and the Arts",
}

// CanonicalPortfolio returns the full portfolio name for a published label.
func CanonicalPortfolio(s string) string {
	if full, ok := portfolioNames[s]; ok {
		return full
	}
	return s
}

var footnote = regexp.MustCompile(`\s*[(\[][a-z][)\]]`)

// Kind maps the published Expense_type to administered, departmental or corporate.
// Corporate Commonwealth entities publish their lines under "Revenue from Government",
// split by funding source.
func Kind(expenseType string) (string, error) {
	t := strings.ToLower(footnote.ReplaceAllString(expenseType, ""))
	switch {
	case strings.Contains(t, "administered"):
		return "administered", nil
	case strings.Contains(t, "departmental"):
		return "departmental", nil
	case strings.Contains(t, "revenue"):
		// Usually "Revenue from Government"; one 2026-27 Defence Housing Australia
		// row has its funding source in this column instead.
		return "corporate", nil
	}
	return "", fmt.Errorf("unknown expense type %q", expenseType)
}

// CleanText normalises whitespace and repairs characters lost in the source's
// encoding (U+FFFD): an apostrophe between letters, otherwise an en dash.
func CleanText(s string) string {
	r := []rune(s)
	for i, c := range r {
		switch {
		case c == ' ':
			r[i] = ' '
		case c == '�':
			if i > 0 && i < len(r)-1 && unicode.IsLetter(r[i-1]) && unicode.IsLetter(r[i+1]) {
				r[i] = '’'
			} else {
				r[i] = '–'
			}
		}
	}
	return strings.Join(strings.Fields(string(r)), " ")
}
