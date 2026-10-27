package investments

import (
	"os"
	"reflect"
	"testing"
)

func parseFile(t *testing.T, name string, cols int) Table {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := Parse(string(b), cols)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func find(t *testing.T, tbl Table, item string) Row {
	t.Helper()
	for _, r := range tbl.Rows {
		if Key(r.Item) == Key(item) {
			return r
		}
	}
	t.Fatalf("%s: no row %q", tbl.Ref, item)
	return Row{}
}

func TestParseBudget(t *testing.T) {
	b, _ := os.ReadFile("testdata/bp1-2026-27.txt")
	if got, want := Years(string(b)), []string{"2025-26", "2026-27", "2027-28", "2028-29", "2029-30"}; !reflect.DeepEqual(got, want) {
		t.Errorf("years = %v, want %v", got, want)
	}
	tbl := parseFile(t, "bp1-2026-27.txt", 6)
	if tbl.Ref != "Table 3.5" || len(tbl.Rows) != 23 {
		t.Errorf("%s: %d rows", tbl.Ref, len(tbl.Rows))
	}
	if got := find(t, tbl, "Snowy Hydro Limited loan").Amounts; !reflect.DeepEqual(got, []int64{-1450, -2900, 0, 0, 150, -4200}) {
		t.Errorf("Snowy loan = %v", got)
	}
	// Wrapped label, and a label that starts with a number.
	find(t, tbl, "Australian apprenticeship support loans")
	find(t, tbl, "100,000 Homes for First Home Buyers")
	if r := find(t, tbl, "Net other"); r.Item != "Net other" {
		t.Errorf("footnote marker kept: %q", r.Item)
	}
	if tbl.Total.Amounts[1] != -32544 {
		t.Errorf("total = %v", tbl.Total.Amounts)
	}

	old := parseFile(t, "bp1-2025-26.txt", 6)
	if old.Ref != "Table 3.4" || old.Total.Amounts[0] != -19076 {
		t.Errorf("%s total = %v", old.Ref, old.Total.Amounts)
	}
}

func TestParseOutcome(t *testing.T) {
	tbl := parseFile(t, "fbo-2025-26.txt", 3)
	if tbl.Ref != "Table 1.8" || tbl.Total.Amounts[1] != -13832 {
		t.Errorf("%s total = %v", tbl.Ref, tbl.Total.Amounts)
	}
	if got := find(t, tbl, "Housing Australia").Amounts; !reflect.DeepEqual(got, []int64{-2455, -1344, 1111}) {
		t.Errorf("Housing Australia = %v", got)
	}
}

func TestKey(t *testing.T) {
	for _, c := range [][2]string{
		{"Snowy Hydro Limited Loan", "snowy hydro limited loan"},
		{"COVID-19 Support for Indonesia -- loan", "covid - 19 support for indonesia - loan"},
		{"COVID-19 Support for Indonesia - loan", "covid - 19 support for indonesia - loan"},
		{"Net other(b)", "net other"},
	} {
		if got := Key(c[0]); got != c[1] {
			t.Errorf("Key(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}
