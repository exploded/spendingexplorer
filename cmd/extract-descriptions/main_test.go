package main

import "testing"

func TestForBudget(t *testing.T) {
	for u, want := range map[string]bool{
		"https://www.health.gov.au/sites/default/files/2026-05/2026-27_health_pbs_2.02_outcome_2_0.pdf": true,
		"https://www.dfat.gov.au/sites/default/files/2008-2009_FA%20T_PBS_Consolidated.pdf":             false,
		"https://www.dfat.gov.au/sites/default/files/2026-2027-pbs.pdf":                                 true,
		"https://www.agriculture.gov.au/sites/default/files/documents/2024-25-daff-paes.PDF":            false,
		"https://www.agriculture.gov.au/sites/default/files/documents/2026-27-daff-pbs.pdf":             true,
		"https://www.dewr.gov.au/download/17602/x/42704/document/pdf":                                   true,
	} {
		if got := forBudget(u, "2026-27"); got != want {
			t.Errorf("forBudget(%s) = %v, want %v", u, got, want)
		}
	}
}
