package grantconnect

import "testing"

var header = []string{"GA ID", "Grant Activity", "Agency", "Publish Date", "Category", "Start Date", "End Date",
	"Value (AUD)", "GO ID", "Recipient Name", "One-off/Ad hoc", "Aggregate Grant Award", "Aggregate Reason",
	"Number of Awards Aggregated", "Last Updated"}

func summary(dateRange string, capped bool) [][]string {
	rows := [][]string{{"Grant Award List"}, {"Criteria Summary"}, {"Date Range", dateRange}}
	if capped {
		rows = append(rows, []string{"Your search returned more than 50000 results. Please narrow the date range and run multiple searches to return all records."})
	}
	return append(rows, header)
}

func TestParseRows(t *testing.T) {
	rows := append(summary("1-Jul-2022 to 31-Dec-2022", false),
		[]string{"GA270901-V1", "Supporting Medication Management", "Department of Health and Aged Care", "44862.0372106481", "Aged Care", "44859", "45230", "24200", "GO5666", "Kewarra Lifestyles Pty Ltd", "No", "No", "", "0", ""},
		[]string{"GA317617", "Workforce", "Department of Health and Aged Care", "45107.5363541667", "Aged Care", "45105", "45107", "105497.98", "", "Twilight House", "Yes", "No", "", "0", ""},
		[]string{"GA317617", "Workforce", "Department of Health and Aged Care", "45107.5363541667", "Aged Care", "45105", "45107", "105497.98", "", "Twilight House", "Yes", "No", "", "0", ""},
	)
	f, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if f.Capped || f.Range != "1-Jul-2022 to 31-Dec-2022" || f.Repeated != 1 || len(f.Awards) != 2 {
		t.Fatalf("file = %+v", f)
	}
	a := f.Awards[0]
	if a.Base != "GA270901" || a.Version != 1 || a.PublishDate != "2022-10-28" || a.StartDate != "2022-10-25" ||
		a.EndDate != "2023-10-31" || a.Value != 24200 || a.GOID != "GO5666" {
		t.Errorf("first award = %+v", a)
	}
	if b := f.Awards[1]; b.Version != 0 || b.Value != 105498 || !b.OneOff {
		t.Errorf("second award = %+v", b)
	}
}

func TestCappedAndMerge(t *testing.T) {
	capped, err := parseRows(append(summary("1-Jul-2022 to 27-Oct-2026", true),
		[]string{"GA1", "A", "X", "45000", "C", "45000", "45100", "999", "", "R", "No", "No", "", "0", ""}))
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Capped {
		t.Fatal("capped export not detected")
	}
	a, _ := parseRows(append(summary("r1", false),
		[]string{"GA2", "A", "X", "45000", "C", "45000", "45100", "100", "", "R", "No", "No", "", "0", ""}))
	b, _ := parseRows(append(summary("r2", false),
		[]string{"GA2-V2", "A", "X", "45200", "C", "45000", "45300", "150", "", "R", "No", "No", "", "0", ""},
		[]string{"GA3", "A", "X", "45200", "C", "45000", "45300", "50", "", "R", "No", "No", "", "0", ""}))
	got := Merge([]*File{capped, a, b})
	if len(got) != 2 || got[0].ID != "GA2-V2" || got[0].Value != 150 || got[1].Base != "GA3" {
		t.Errorf("merged = %+v", got)
	}
}

func TestRecipientKey(t *testing.T) {
	if a, b := RecipientKey("BAE JUICE PTY LTD"), RecipientKey("Bae Juice Pty. Ltd."); a != b || a != "bae juice" {
		t.Errorf("keys %q and %q, want both %q", a, b, "bae juice")
	}
	if got := RecipientKey("The University of Sydney"); got != "university of sydney" {
		t.Errorf("leading \"the\" kept: %q", got)
	}
	k := RecipientKey("Leo Baeck Centre for Progressive Judaism Inc.")
	if s := RecipientSlug(k); s != "leo-baeck-centre-for-progressive-judaism" || KeyFromSlug(s) != k {
		t.Errorf("slug %q does not reverse to %q", s, k)
	}
}
