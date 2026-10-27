package pbs

import "testing"

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in      string
		dollars int64
		nfp, ok bool
	}{
		{"6000", 6_000_000, false, true},
		{"36,746,225", 36_746_225_000, false, true},
		{"-30000", -30_000_000, false, true},
		{"(1.5)", -1_500, false, true},
		{"nfp", 0, true, true},
		{"-", 0, false, true},
		{"", 0, false, false},
	}
	for _, c := range cases {
		v, ok, err := ParseAmount(c.in)
		if err != nil || ok != c.ok || v.Dollars != c.dollars || v.NotPublished != c.nfp {
			t.Errorf("ParseAmount(%q) = %+v, %v, %v", c.in, v, ok, err)
		}
	}
	if _, _, err := ParseAmount("abc"); err == nil {
		t.Error("expected error for non-numeric amount")
	}
}

func TestKind(t *testing.T) {
	for in, want := range map[string]string{
		"Administered expenses":                    "administered",
		"Departmental Expenses":                    "departmental",
		"Annual administered expenses":             "administered",
		"Revenue from Government (a) (b)":          "corporate",
		"Revenues from other independent sources ": "corporate",
	} {
		if got, err := Kind(in); err != nil || got != want {
			t.Errorf("Kind(%q) = %q, %v", in, got, err)
		}
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"Australia�s marine":  "Australia’s marine",
		"Revenue � other (c)": "Revenue – other (c)",
		"Program 3.2:  NDIS ": "Program 3.2: NDIS",
	} {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q", in, got)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		line Line
		rule string
	}{
		{Line{Kind: "corporate", LineItem: "Payment from related entities"}, "corporate-commonwealth-funded"},
		{Line{Kind: "corporate", LineItem: "Ordinary annual services (Appropriation Bill (No. 1) and Supply Bill (No. 1))"}, "corporate-commonwealth-funded"},
		{Line{Kind: "corporate", LineItem: "Expenses not requiring appropriation in the Budget year (a)"}, "line-item"},
		{Line{Kind: "corporate", LineItem: "Contributions from state and territory governments"}, "line-item"},
		{Line{Kind: "administered", Entity: "Department of the Treasury", LineItem: "Medicare Guarantee Fund Special Account"}, "medicare-guarantee-fund-credit"},
		{Line{Kind: "administered", Entity: "Department of Health and Aged Care", LineItem: "Medicare Guarantee Fund - medical benefits"}, "line-item"},
		{Line{Kind: "departmental", LineItem: "Total Expenditure"}, "subtotal"},
		{Line{Kind: "administered", Entity: "Australian Taxation Office", Program: "Program 1.17: Bad & Doubtful Debts & Remissions", LineItem: "Expenses not requiring appropriation in the Budget year"}, "tax-bad-debts"},
		{Line{Kind: "departmental", Program: "Totals for Outcome 1", LineItem: "Departmental appropriation"}, "subtotal"},
	}
	for _, c := range cases {
		if got := Classify(c.line).RuleID; got != c.rule {
			t.Errorf("Classify(%q) = %q, want %q", c.line.LineItem, got, c.rule)
		}
	}
}

func TestEstimateType(t *testing.T) {
	for fy, want := range map[string]string{"2024-25": "estimated_actual", "2025-26": "budget", "2027-28": "forward_estimate"} {
		if got := EstimateType("2025-26", fy); got != want {
			t.Errorf("EstimateType(%s) = %s", fy, got)
		}
	}
}
