package transparency

import (
	"strings"
	"testing"
)

const spendCSV = bom + ",Alpha Agency,,,,Beta Office,\n" +
	",2024-25,,2023-24,,2024-25,\n" +
	",Number,Expenditure $'000 (GST inc),Number,Expenditure $'000 (GST inc),Number,Expenditure $'000 (GST inc)\n" +
	"New contracts entered into during the reporting period,3,\"1,200\",2,50,,\n" +
	"Ongoing contracts entered into during a previous reporting period,1,100,0,0,,\n" +
	"Total,4,\"1,300\",2,50,,\n"

func TestParseSpendPivot(t *testing.T) {
	got, err := ParseSpend(strings.NewReader(spendCSV), Consultancy)
	if err != nil {
		t.Fatal(err)
	}
	// Beta Office has no total, so it is skipped.
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	a := got[0]
	if a.Entity != "Alpha Agency" || a.Year != "2024-25" || a.Amount != 1_300_000 || a.Contracts != 4 ||
		a.NewAmount != 1_200_000 || a.NewContracts != 3 || a.OngoingAmount != 100_000 {
		t.Errorf("first row = %+v", a)
	}
	if b := got[1]; b.Entity != "Alpha Agency" || b.Year != "2023-24" || b.Amount != 50_000 {
		t.Errorf("second row = %+v", b)
	}
}

func TestParseOrgs(t *testing.T) {
	in := bom + "Entity,Reporting Period,Name of Organisation,Organisation ABN,Expenditure $'000(GST inc)\n" +
		"Alpha Agency,2024-25,Acme Pty Ltd,51 194 660 183,114\n" +
		"Alpha Agency,2024-25,No Amount Pty Ltd,,\n" +
		`"Alpha Agency","2024-25",""QIC Limited ACN 130 539 123"","95 942 373 762","560"` + "\n"
	got, err := ParseOrgs(strings.NewReader(in), NonConsultancy)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ABN != "51194660183" || got[0].Amount != 114_000 || got[0].Kind != NonConsultancy {
		t.Fatalf("got %+v", got)
	}
	if q := got[1]; q.Name != "QIC Limited ACN 130 539 123" || q.Amount != 560_000 {
		t.Errorf("double-wrapped row = %+v", q)
	}
}

func TestCheckSpend(t *testing.T) {
	cases := []struct {
		name     string
		s        Spend
		expenses int64
		flagged  bool
	}{
		{"dollars entered as thousands", Spend{Amount: 1_401_300_000, Contracts: 16}, 25_000_000, true},
		{"large but within twice expenses", Spend{Amount: 40_000_000_000, Contracts: 30_000}, 50_000_000_000, false},
		{"unmatched entity, implausible average", Spend{Amount: 750_000_000, Contracts: 7}, 0, true},
		{"unmatched entity, normal average", Spend{Amount: 9_000_000, Contracts: 40}, 0, false},
		{"unmatched entity, no contract count", Spend{Amount: 750_000_000}, 0, false},
		{"listed twice", Spend{Amount: 5_000_000, Contracts: 60, Repeated: true}, 3_000_000_000, true},
		{"consultancy average under the ceiling", Spend{Kind: Consultancy, Amount: 49_770_000, Contracts: 4}, 45_000_000, true},
		{"large but ordinary consultancy", Spend{Kind: Consultancy, Amount: 24_810_000, Contracts: 14}, 2_000_000_000, false},
	}
	for _, c := range cases {
		if got := CheckSpend(c.s, c.expenses) != ""; got != c.flagged {
			t.Errorf("%s: flagged = %v, want %v", c.name, got, c.flagged)
		}
	}
}

func TestCheckOrg(t *testing.T) {
	if CheckOrg(Org{Amount: 551_000_000}, 45_000_000, 0, "") == "" {
		t.Error("share over twice total expenses not flagged")
	}
	if CheckOrg(Org{Amount: 2_000_000}, 0, 1_500_000, "") == "" {
		t.Error("share over the reported total not flagged")
	}
	if CheckOrg(Org{Amount: 29_000_000}, 45_000_000, 0, "total looks wrong") == "" {
		t.Error("share in a year whose total looks wrong not flagged")
	}
	if CheckOrg(Org{Amount: 300_000}, 25_000_000, 1_400_000, "") != "" {
		t.Error("ordinary share flagged")
	}
}
