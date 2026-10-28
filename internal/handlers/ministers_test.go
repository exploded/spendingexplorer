package handlers

import (
	"sort"
	"testing"

	"spendingexplorer/internal/db"
)

func TestLeadTitle(t *testing.T) {
	for _, c := range []struct {
		portfolio, title string
		want             bool
	}{
		{"Health, Disability and Ageing", "Minister for Health and Ageing", true},
		{"Health, Disability and Ageing", "Minister for Aged Care and Seniors", false},
		{"Treasury", "Treasurer", true},
		{"Treasury", "Assistant Treasurer", false},
		{"Prime Minister and Cabinet", "Prime Minister", true},
		{"Attorney-General's", "Attorney-General", true},
		{"Infrastructure, Transport, Regional Development, Communications, Sport and the Arts", "Minister for the Arts", false},
		{"Infrastructure, Transport, Regional Development, Communications, Sport and the Arts", "Minister for Infrastructure, Transport, Regional Development and Local Government", true},
		{"Defence", "Assistant Minister for Defence", false},
	} {
		if got := leadTitle(c.portfolio, c.title); got != c.want {
			t.Errorf("leadTitle(%q, %q) = %v", c.portfolio, c.title, got)
		}
	}
}

// Cabinet first, the minister named for the portfolio leading, assistant
// ministers last.
func TestMinisterOrder(t *testing.T) {
	ms := []ministerView{
		{Name: "Assistant", assistant: true, start: "2022-01-01"},
		{Name: "Outer", start: "2025-01-01"},
		{Name: "Cabinet other", Cabinet: true, start: "2022-01-01"},
		{Name: "Cabinet lead", Cabinet: true, lead: true, start: "2025-01-01"},
	}
	sort.Slice(ms, func(i, j int) bool { return ministerBefore(ms[i], ms[j]) })
	want := []string{"Cabinet lead", "Cabinet other", "Outer", "Assistant"}
	for i, m := range ms {
		if m.Name != want[i] {
			t.Fatalf("order = %v, want %v", ms, want)
		}
	}
}

func TestBio(t *testing.T) {
	member := db.PortfolioMinistersRow{Party: "Australian Labor Party", Chamber: "Member", Electorate: "Hindmarsh", State: "South Australia", InParliament: 1, ServiceStart: "2007-11-24"}
	if got := bio(member); got != "Australian Labor Party · Member for Hindmarsh, South Australia · in Parliament since 2007" {
		t.Errorf("member: %q", got)
	}
	senator := db.PortfolioMinistersRow{Party: "Australian Labor Party", Chamber: "Senator", State: "South Australia", InParliament: 1, ServiceStart: "2002-07-01"}
	if got := bio(senator); got != "Australian Labor Party · Senator for South Australia · in Parliament since 2002" {
		t.Errorf("senator: %q", got)
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Sam Rae": "SR", "Luke Howarth": "LH", "Anne Aly": "AA", "Jo Van Der Berg": "JB", "Cher": "C"} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}
