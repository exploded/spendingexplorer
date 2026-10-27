package corpgroup

import "testing"

func TestKey(t *testing.T) {
	for name, want := range map[string]string{
		"BOEING DEFENCE AUSTRALIA LTD":                             "BOEING",
		"THE BOEING COMPANY":                                       "BOEING",
		"Accenture Australia Pty Ltd":                              "ACCENTURE",
		"Australian Healthcare Associates":                         "HEALTHCARE",
		"Data#3 Limited Formerly: Data #3 Business":                "DATA#3",
		"AHA PTY LTD T/A AUSTRALIAN HEALTHCARE ASSOCIATE":          "AHA",
		"McDonald's Australia Ltd":                                 "MCDONALDS",
		"A B Smith":                                                "",
		"The Trustee for 150 Lonsdale Street Trust":                "TRUSTEE",
		"(Formerly Acme) Widgets":                                  "",
		"THE TRUST COMPANY (AUSTRALIA) LIMITED AS TRUSTEE FOR XYZ": "TRUST",
	} {
		if got := Key(name); got != want {
			t.Errorf("Key(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestActsForTrust(t *testing.T) {
	for name, want := range map[string]bool{
		"The Trust Company (Australia) Ltd A The 747 Collins St Trust and The Tr": true,
		"The Trust Company (Australia) Limited as trustee":                        true,
		"The Trust Company Limited as custodian for Goodman Australia":            true,
		"PERPETUAL CORPORATE TRUST LTD ATF ABC":                                   true,
		"Scentre Managment Limited responsib Entity the Carindale Property Trust": true,
		"The Trust Company (Australia) Limited":                                   false,
		"ACCENTURE AUSTRALIA PTY LTD":                                             false,
	} {
		if got := ActsForTrust(name); got != want {
			t.Errorf("ActsForTrust(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	sent := []Cluster{
		{Key: "BOEING", Members: []Member{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}},
		{Key: "DATA", Members: []Member{{ID: 5}, {ID: 6}}},
		{Key: "SECURE", Members: []Member{{ID: 7}, {ID: 8}}},
	}
	text := `{"clusters":[
	  {"key":"BOEING","groups":[
	    {"name":"Boeing","confidence":"high","members":[1,2,99]},
	    {"name":"Again","confidence":"high","members":[2,3]},
	    {"name":"Solo","confidence":"low","members":[4]}]},
	  {"key":"DATA","groups":[]},
	  {"key":"NOTSENT","groups":[{"name":"X","confidence":"high","members":[7,8]}]}]}`
	got, err := Parse(text, sent)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{1: "g1", 2: "g1", 3: "", 4: "", 5: "", 6: ""}
	if len(got) != len(want) {
		t.Fatalf("got %d assignments, want %d: %+v", len(got), len(want), got)
	}
	for _, a := range got {
		if l, ok := want[a.SupplierID]; !ok || l != a.Label {
			t.Errorf("supplier %d: label %q, want %q", a.SupplierID, a.Label, l)
		}
	}
}
