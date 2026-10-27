package classify

import (
	"strings"
	"testing"
)

func TestPromptAndSchema(t *testing.T) {
	p, err := Prompt([]Item{{CNID: "CN1", Description: "LH - ETG - Senior Security Specialist", Agency: "AEC", Supplier: "Exclaim IT Pty Ltd"}})
	if err != nil || !strings.Contains(p, `"id": "CN1"`) {
		t.Fatalf("prompt = %q, %v", p, err)
	}
	for _, c := range Categories {
		if !strings.Contains(System(), c.ID+": ") {
			t.Errorf("system prompt lacks %s", c.ID)
		}
	}
	enum := Schema()["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["category"].(map[string]any)["enum"].([]any)
	if len(enum) != len(Categories) {
		t.Errorf("schema enum has %d categories", len(enum))
	}
}

func TestParseKeepsOnlySentIDsAndKnownCategories(t *testing.T) {
	sent := []Item{{CNID: "CN1"}, {CNID: "CN2"}}
	text := `{"results":[
		{"id":"CN1","category":"labour_hire","confidence":"high"},
		{"id":"CN1","category":"consultancy","confidence":"low"},
		{"id":"CN2","category":"astrology","confidence":"high"},
		{"id":"CN9","category":"legal","confidence":"high"}]}`
	got, err := Parse(text, sent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CNID != "CN1" || got[0].Category != "labour_hire" {
		t.Errorf("Parse = %+v", got)
	}
	if _, err := Parse("not json", sent); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestInputHashChangesWithDescription(t *testing.T) {
	a := InputHash(Item{Description: "Network Engineer"})
	b := InputHash(Item{Description: "Network Engineer extension"})
	if a == b {
		t.Error("hash did not change")
	}
}
