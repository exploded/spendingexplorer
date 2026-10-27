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
