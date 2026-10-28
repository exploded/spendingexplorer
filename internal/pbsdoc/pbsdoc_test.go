package pbsdoc

import (
	"os"
	"strings"
	"testing"
)

func TestExtractHealthOutcome2(t *testing.T) {
	b, err := os.ReadFile("testdata/health-outcome2.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Objective{}
	for _, o := range Extract(string(b)) {
		got[o.Code] = o
	}
	for _, o := range got {
		t.Logf("%s p%d: %s", o.Heading, o.Page, o.Text)
	}
	mb, ok := got["2.1"]
	if !ok {
		t.Fatalf("2.1 not found; got %v", got)
	}
	if mb.Heading != "Program 2.1: Medical Benefits" || !strings.HasPrefix(mb.Text, "Deliver a modern, sustainable Medicare Benefits Schedule") {
		t.Errorf("2.1 = %+v", mb)
	}
	if strings.Contains(mb.Text, "Key Activity") {
		t.Errorf("2.1 ran past its block: %q", mb.Text)
	}
	for _, code := range []string{"2.2", "2.3", "2.4", "2.5", "2.6", "2.7"} {
		if _, ok := got[code]; !ok {
			t.Errorf("%s not found", code)
		}
	}
}

// A linked programs section quotes other entities' program headings, often
// across a page break; none of them is this document's objective.
func TestExtractSkipsLinkedPrograms(t *testing.T) {
	text := `Linked Programs
 Other Commonwealth Entities that contribute to Outcome 1
 Department of the Treasury (Treasury)
 Program 1.4: Commonwealth-State Financial Relations
 Treasury provides financial assistance to state and territory governments as part of the
 Federal Financial Relations Framework, including OzFoodNet and mosquito control programs.
` + "\f" + `Other Commonwealth Entities that contribute to Outcome 1 (continued)
 Program 1.1: Food Regulatory Activity and Services to the Minister and Parliament
 FSANZ contributes to the CDC's work through protection of public health and safety by
 developing food standards for implementation by the states and territories.

Budgeted expenses for Outcome 1

Program 1.1: Analyse, understand and advise to support health protection
Support timely and consistent national public health planning and action including through
the collection, analysis, and sharing of public health information and data.
Key activities
`
	got := Extract(text)
	if len(got) != 1 || !strings.HasPrefix(got[0].Heading, "Program 1.1: Analyse") {
		t.Errorf("got %+v, want only the entity's own Program 1.1", got)
	}
}

// A portfolio document covers several entities, each numbering its programs
// from 1.1, so two programs can share a number.
func TestExtractKeepsSameNumberAcrossEntities(t *testing.T) {
	text := `Program 1.4 – Commonwealth-State Financial Relations

The objective of the program is to make payments which provide financial support to the States and
Territories on a wide range of activities under the Intergovernmental Agreement.
` + "\f" + `Program 1.4: Australian Charities and Not-for-profits Commission
The Australian Charities and Not-for-profits Commission (ACNC) is the independent national
regulator of charities, with statutory functions and regulatory powers.
Key activities
`
	var headings []string
	for _, o := range Extract(text) {
		headings = append(headings, o.Heading)
	}
	want := []string{"Program 1.4: Commonwealth-State Financial Relations", "Program 1.4: Australian Charities and Not-for-profits Commission"}
	if strings.Join(headings, "|") != strings.Join(want, "|") {
		t.Errorf("headings = %q, want %q", headings, want)
	}
}
