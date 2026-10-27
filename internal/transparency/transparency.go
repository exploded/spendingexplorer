// Package transparency parses data set exports from the Transparency Portal's
// Data Explorer (transparency.gov.au): what each non-corporate entity reported
// in its annual report as spent on consultancy and non-consultancy contracts,
// and the organisations that received the largest shares. The portal publishes
// amounts in $'000, GST inclusive, on a cash basis.
//
// Entities enter these figures themselves, and some have entered dollars where
// thousands were asked for. Check flags figures that cannot be right; flagged
// figures are shown with the reason and never used in a total.
package transparency

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"spendingexplorer/internal/explore"
)

// Kind is the contract type a data set covers.
type Kind string

const (
	Consultancy    Kind = "consultancy"
	NonConsultancy Kind = "non_consultancy"
)

// Spend is one entity's reported expenditure for one reporting year.
// Amounts are whole dollars.
type Spend struct {
	Entity, Year     string
	Kind             Kind
	NewContracts     int64
	NewAmount        int64
	OngoingContracts int64
	OngoingAmount    int64
	Contracts        int64
	Amount           int64
	Repeated         bool // the export lists more than one figure for this entity and year
}

// Org is one organisation in an entity's list of largest recipients.
type Org struct {
	Entity, Year, Name, ABN string
	Kind                    Kind
	Amount                  int64
}

// ParseSpend reads an "Expenditure on Reportable … Contracts" export. The
// export is pivoted: row 1 names each entity once over its columns, row 2 the
// reporting period over a Number and Expenditure pair, row 3 the measure, and
// later rows hold new, ongoing and total contracts. Entity-years with no total
// expenditure are skipped.
func ParseSpend(r io.Reader, kind Kind) ([]Spend, error) {
	rows, err := readAll(r)
	if err != nil {
		return nil, err
	}
	if len(rows) < 4 {
		return nil, fmt.Errorf("expenditure export: %d rows, want at least 4", len(rows))
	}
	line := map[string]int{}
	for i, row := range rows[3:] {
		if len(row) == 0 {
			continue
		}
		switch label := strings.ToLower(strings.TrimSpace(row[0])); {
		case strings.HasPrefix(label, "new contracts"):
			line["new"] = i + 3
		case strings.HasPrefix(label, "ongoing contracts"):
			line["ongoing"] = i + 3
		case label == "total":
			line["total"] = i + 3
		}
	}
	if _, ok := line["total"]; !ok {
		return nil, fmt.Errorf("expenditure export: no Total row")
	}
	cell := func(row, col int) string {
		if row == 0 || row >= len(rows) || col >= len(rows[row]) {
			return ""
		}
		return rows[row][col]
	}

	var out []Spend
	seen := map[[2]string]int{} // entity and year -> index in out
	entity, year := "", ""
	for col := 1; col < len(rows[0]); col++ {
		if v := strings.TrimSpace(rows[0][col]); v != "" {
			entity = v
		}
		if v := strings.TrimSpace(cell(1, col)); v != "" {
			year = v
		}
		if !strings.HasPrefix(cell(2, col), "Expenditure") || col == 0 {
			continue
		}
		amount, ok := thousands(cell(line["total"], col))
		if !ok {
			continue
		}
		s := Spend{Entity: entity, Year: year, Kind: kind, Amount: amount}
		s.Contracts, _ = count(cell(line["total"], col-1))
		s.NewContracts, _ = count(cell(line["new"], col-1))
		s.NewAmount, _ = thousands(cell(line["new"], col))
		s.OngoingContracts, _ = count(cell(line["ongoing"], col-1))
		s.OngoingAmount, _ = thousands(cell(line["ongoing"], col))
		// When a year is listed twice, keep the last figure and mark it.
		if i, ok := seen[[2]string{entity, year}]; ok {
			s.Repeated = true
			out[i] = s
			continue
		}
		seen[[2]string{entity, year}] = len(out)
		out = append(out, s)
	}
	return out, nil
}

// ParseOrgs reads an "Organisations Receiving a Share of …" export, which has
// one row per entity, year and organisation.
func ParseOrgs(r io.Reader, kind Kind) ([]Org, error) {
	rows, err := readAll(r)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("organisations export is empty")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	need := []string{"entity", "reporting period", "name of organisation", "organisation abn"}
	for _, n := range need {
		if _, ok := col[n]; !ok {
			return nil, fmt.Errorf("organisations export: no %q column", n)
		}
	}
	amountCol := -1
	for h, i := range col {
		if strings.HasPrefix(h, "expenditure") {
			amountCol = i
		}
	}
	if amountCol < 0 {
		return nil, fmt.Errorf("organisations export: no expenditure column")
	}
	var out []Org
	for _, row := range rows[1:] {
		if len(row) <= amountCol {
			continue
		}
		amount, ok := thousands(row[amountCol])
		if !ok {
			continue
		}
		out = append(out, Org{
			Entity: strings.TrimSpace(row[col["entity"]]),
			Year:   strings.TrimSpace(row[col["reporting period"]]),
			Name:   strings.TrimSpace(row[col["name of organisation"]]),
			ABN:    strings.ReplaceAll(strings.TrimSpace(row[col["organisation abn"]]), " ", ""),
			Kind:   kind,
			Amount: amount,
		})
	}
	return out, nil
}

// An entity cannot spend more on contracts than its total expenses, but
// contract spending can include capital purchases and entities grow, so the
// ceiling allows twice the largest yearly total. Entering dollars as
// thousands overstates a figure 1,000-fold, far past this margin.
const ceilingMargin = 2

// Averages per contract that suggest the same error. Reported consultancy
// averaged at most about $1.8 million per contract from 2021-22 to 2024-25,
// so $5 million is a wide margin. Other contracts vary more, so their limit
// applies only when there are no Budget figures to compare.
const (
	maxConsultancyAverage = 5_000_000
	maxAverage            = 25_000_000
)

// CheckSpend returns why a reported figure looks wrong, or "" if it passes.
// expenses is the entity's largest yearly total expenses in the Budget papers
// held, or 0 when the entity is not matched to them.
func CheckSpend(s Spend, expenses int64) string {
	if expenses > 0 && s.Amount > ceilingMargin*expenses {
		return fmt.Sprintf("More than twice the entity’s largest yearly total expenses in the Budget papers (%s). It may have been entered in dollars instead of thousands.", explore.Money(expenses))
	}
	if s.Kind == Consultancy && s.Contracts > 0 && s.Amount/s.Contracts > maxConsultancyAverage {
		return "Averages more than $5 million per consultancy contract, far above any other entity. It may have been entered in dollars instead of thousands."
	}
	if expenses == 0 && s.Contracts > 0 && s.Amount/s.Contracts > maxAverage {
		return "Averages more than $25 million per contract, which is unusual. It may have been entered in dollars instead of thousands."
	}
	if s.Repeated {
		return "The portal publishes more than one figure for this year; this is the last one listed."
	}
	return ""
}

// CheckOrg returns why an organisation's reported share looks wrong, or "".
// total is the entity's reported total for the same year and kind, and
// totalNote the reason that total looks wrong, if it does: an entity that
// misreported its total usually misreported its list the same way.
func CheckOrg(o Org, expenses, total int64, totalNote string) string {
	if expenses > 0 && o.Amount > ceilingMargin*expenses {
		return fmt.Sprintf("More than twice the entity’s largest yearly total expenses in the Budget papers (%s). It may have been entered in dollars instead of thousands.", explore.Money(expenses))
	}
	if totalNote != "" {
		return "The entity’s total for this year looks wrong, so this list may be affected too."
	}
	if total > 0 && o.Amount > total {
		return fmt.Sprintf("Larger than the entity’s reported total for the year (%s).", explore.Money(total))
	}
	return ""
}

// bom is the UTF-8 byte order mark the portal writes at the start of exports.
const bom = "\xef\xbb\xbf"

// doubleWrapped matches a field the portal wraps in two pairs of quotes
// without escaping them, as in ,""QIC Limited …"",
var doubleWrapped = regexp.MustCompile(`(^|,)""([^",][^"]*)""(,|\r?$)`)

// readAll drops the byte order mark before parsing (the exports start with a
// quoted field, which the CSV reader rejects after a mark) and repairs
// double-wrapped fields. Any other malformed quoting is an error.
func readAll(r io.Reader) ([][]string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimPrefix(string(b), bom), "\n")
	for i, l := range lines {
		lines[i] = doubleWrapped.ReplaceAllString(l, `$1"$2"$3`)
	}
	cr := csv.NewReader(strings.NewReader(strings.Join(lines, "\n")))
	cr.FieldsPerRecord = -1
	return cr.ReadAll()
}

// thousands converts a published $'000 figure to whole dollars.
func thousands(s string) (int64, bool) {
	f, ok := number(s)
	return int64(math.Round(f * 1000)), ok
}

func count(s string) (int64, bool) {
	f, ok := number(s)
	return int64(math.Round(f)), ok
}

func number(s string) (float64, bool) {
	s = strings.NewReplacer(",", "", "$", "", " ", "").Replace(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}
