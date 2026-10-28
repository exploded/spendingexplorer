package main

import "testing"

func TestTitle(t *testing.T) {
	for _, c := range []struct {
		r    record
		want string
	}{
		{record{Role: "Minister", Prep: "for", Entity: "Health and Ageing"}, "Minister for Health and Ageing"},
		{record{Role: "Treasurer"}, "Treasurer"},
		{record{Role: "Vice-President", Prep: "of the", Entity: "Executive Council"}, "Vice-President of the Executive Council"},
		{record{Role: "Minister Assisting", Prep: "the Minister for the", Entity: "National Disability Insurance Scheme"}, "Minister Assisting the Minister for the National Disability Insurance Scheme"},
	} {
		if got := c.r.title(); got != c.want {
			t.Errorf("title(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}

// An appointment that spans ministries is one holding, named after the
// latest ministry; records before the first ministry are left out.
func TestHoldings(t *testing.T) {
	recs := []record{
		{MID: 72, PHID: "X", Role: "Minister", Prep: "for", Entity: "Defence", RDateStart: "2018-08-28", Ministry: "Morrison Ministry"},
		{MID: 74, PHID: "HWK", Role: "Cabinet Minister", RDateStart: "2022-06-01", Ministry: "Albanese Ministry"},
		{MID: 75, PHID: "HWK", Role: "Cabinet Minister", RDateStart: "2022-06-01", Ministry: "Albanese Ministry (2nd)"},
		{MID: 74, PHID: "HWK", Role: "Minister", Prep: "for", Entity: "Health and Aged Care", RDateStart: "2022-06-01", RDateEnd: "2025-05-13", Ministry: "Albanese Ministry"},
	}
	got := holdings(recs, 73)
	if len(got) != 2 {
		t.Fatalf("got %d holdings, want 2: %v", len(got), got)
	}
	if h := got[holdingKey{"HWK", "Cabinet Minister", "2022-06-01"}]; h.ministry != "Albanese Ministry (2nd)" || h.end != "" {
		t.Errorf("cabinet holding = %+v", h)
	}
	if h := got[holdingKey{"HWK", "Minister for Health and Aged Care", "2022-06-01"}]; h.end != "2025-05-13" {
		t.Errorf("health holding = %+v", h)
	}
}

func TestCasedName(t *testing.T) {
	for in, want := range map[string]string{
		"BUTLER":            "Butler",
		"McALLISTER":        "McAllister",
		"O'NEIL":            "O'Neil",
		"THISTLETHWAITE":    "Thistlethwaite",
		"SMITH-JONES":       "Smith-Jones",
		"van MANEN":         "van Manen",
		"McBAIN":            "McBain",
		"Mc":                "Mc",
		"de BRUYN":          "de Bruyn",
		"HANSON-YOUNG":      "Hanson-Young",
		"D'ATH":             "D'Ath",
		"O’CONNOR":          "O’Connor",
		"FIERRAVANTI-WELLS": "Fierravanti-Wells",
	} {
		if got := casedName(in); got != want {
			t.Errorf("casedName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPersonName(t *testing.T) {
	in := individual{GivenName: "Anthony", PreferredName: "Tony", DisplayName: "BURKE, the Hon. Anthony (Tony) Stephen"}
	if got := personName(in, ""); got != "Tony Burke" {
		t.Errorf("no label: %q", got)
	}
	if got := personName(in, "Tony Burke"); got != "Tony Burke" {
		t.Errorf("label: %q", got)
	}
	// A label that doesn't end with the family name isn't trusted.
	if got := personName(in, "Anthony Burke (politician)"); got != "Tony Burke" {
		t.Errorf("odd label: %q", got)
	}
	mc := individual{GivenName: "Jennifer", PreferredName: "Jenny", DisplayName: "McALLISTER, the Hon. Jennifer (Jenny) Ryll"}
	if got := personName(mc, ""); got != "Jenny McAllister" {
		t.Errorf("McAllister: %q", got)
	}
}
