package austender

import "testing"

func TestLooksLikePerson(t *testing.T) {
	given := map[string]bool{"NAMMO": true, "SCOTT": true, "PIETER": true, "MICHELLE": true, "MATTHEW": true, "TRENT": true, "MICHAEL": true, "GREG": true}
	people := []string{
		"Scott Reasoner",
		"Pieter Mayne",
		"B J Whelan",
		"JN Phelps",
		"MICHELLE ENGLAND BARRISTER-AT-LAW",
		"MATTHEW THOMAS BRADY KC",
		"Dr Scott Reasoner",
		"Shera, Michael Stanley",
		"TRENT LEIGH MASON ta NORTHENTIC",
		"GREG O'MAHONEY",
	}
	for _, n := range people {
		if !LooksLikePerson(n, given) {
			t.Errorf("LooksLikePerson(%q) = false, want true", n)
		}
	}
	orgs := []string{
		"KPMG",
		"NAMMO RAUFOSS A/S",
		"FAJARBARU BUILDER SDN BHD",
		"OCCAR-EA",
		"NATO SEASPARROW SURFACE MISSILE SYSTEM PROJECT",
		"Global Affairs Canada",
		"Webb Henderson",         // no known given name
		"Scott Reasoner Pty Ltd", // legal form
		"Scott & Reasoner",       // partnership
		"Michael Page International",
		"PIETER MAYNE 2",
		"SCOTT REYES S.A.S.",
		"The Scott",
	}
	for _, n := range orgs {
		if LooksLikePerson(n, given) {
			t.Errorf("LooksLikePerson(%q) = true, want false", n)
		}
	}
}
