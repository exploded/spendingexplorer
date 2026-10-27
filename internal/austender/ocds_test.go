package austender

import (
	"os"
	"testing"
)

func load(t *testing.T, name string) *Page {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePage(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestContracts(t *testing.T) {
	p := load(t, "page.json")
	byID := map[string]Contract{}
	for _, r := range p.Releases {
		cs, err := r.Flatten()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			byID[c.CNID] = c
		}
	}
	c := byID["CN4258593"]
	if c.Agency != "Australian Electoral Commission" || c.Supplier != "Exclaim IT Pty Ltd" || c.SupplierABN != "97109189059" {
		t.Errorf("parties: %+v", c)
	}
	if c.Value != 424371 || c.Method != "open" || c.UNSPSC != "80111600" {
		t.Errorf("value/method/unspsc: %d %s %s", c.Value, c.Method, c.UNSPSC)
	}
	// 2026-06-17T14:00:00Z is midnight 18 June in Canberra.
	if c.StartDate != "2026-06-18" || c.EndDate != "2027-06-30" {
		t.Errorf("dates: %s to %s", c.StartDate, c.EndDate)
	}
	a := byID["CN4258440"]
	if !a.Amendment || len(a.AmendmentIDs) != 1 || a.Method != "limited" || a.Exemption == "" {
		t.Errorf("amendment: %+v", a)
	}
}

func TestHistory(t *testing.T) {
	p := load(t, "byid.json")
	var values []int64
	for _, r := range p.Releases {
		cs, _ := r.Flatten()
		values = append(values, cs[0].Value)
	}
	if len(values) != 2 || values[0] != 108800 || values[1] != 153800 {
		t.Errorf("history values = %v", values)
	}
}

func TestNormaliseABN(t *testing.T) {
	for in, want := range map[string]string{"97 109 189 059": "97109189059", "123": "", "": ""} {
		if got := normaliseABN(in); got != want {
			t.Errorf("normaliseABN(%q) = %q", in, got)
		}
	}
}
