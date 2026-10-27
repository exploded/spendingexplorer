// Package classify builds Batch API requests that classify AusTender contract
// descriptions, and parses the results. Agencies' own categories (UNSPSC
// codes, and the consultancy flag on AusTender's website) are often too broad
// or inconsistent; an LLM reading the description gives a second, labelled
// view. Every result is stored with the model and prompt version.
package classify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	Task          = "professional-services"
	PromptVersion = "v1"
	Model         = "claude-haiku-5-5"
	PerRequest    = 100 // contracts per request; many per request keeps cost down
)

// Category is one class a contract can be put in.
type Category struct {
	ID    string
	Label string
	Means string // the definition given to the model
}

// Categories for UNSPSC segment 80, management and business professional
// services. The consultancy definition follows the Department of Finance's
// distinction between consultants and contractors.
var Categories = []Category{
	{"consultancy", "Consultancy", "Independent expert advice, review, evaluation, research or strategy that informs a decision by the agency."},
	{"labour_hire", "Labour hire / contractor", "A person or team filling a role the agency directs, such as a named position, an engineer, analyst or project officer."},
	{"it_services", "IT services", "Software, systems implementation, managed IT services, cyber security operations or technical support."},
	{"legal", "Legal", "Legal advice, counsel, litigation or legal services."},
	{"audit_finance", "Audit, accounting and finance", "Audit, assurance, accounting, actuarial or financial services."},
	{"training", "Training and education", "Training courses, coaching or education delivery."},
	{"recruitment", "Recruitment", "Recruitment, executive search or candidate assessment."},
	{"communications", "Communications and marketing", "Advertising, media, communications, events or design."},
	{"service_delivery", "Outsourced service delivery", "Delivering a service to the public or to staff on the agency's behalf, such as call centres, case management or administration."},
	{"other", "Other", "Anything that fits none of the above, or a description too vague to classify."},
}

// Item is one contract sent for classification.
type Item struct {
	CNID        string `json:"id"`
	Description string `json:"description"`
	Agency      string `json:"agency"`
	Supplier    string `json:"supplier"`
}

// Result is the model's classification of one contract.
type Result struct {
	CNID       string `json:"id"`
	Category   string `json:"category"`
	Confidence string `json:"confidence"` // high | medium | low
}

// System is the instruction sent with every request. Changing it requires a
// new PromptVersion so stored results stay comparable.
func System() string {
	var b strings.Builder
	b.WriteString("You classify Australian Government contract notices from AusTender by the kind of work bought. ")
	b.WriteString("Each notice has a short description written by the agency, the agency name and the supplier name. ")
	b.WriteString("Choose one category per contract from this list:\n\n")
	for _, c := range Categories {
		fmt.Fprintf(&b, "- %s: %s\n", c.ID, c.Means)
	}
	b.WriteString("\nUse the description first and the supplier name only as supporting evidence. ")
	b.WriteString("Agency abbreviations such as LH (labour hire) or ICT are common. ")
	b.WriteString("Set confidence to low when the description is too short or vague to be sure. ")
	b.WriteString("Return one result for every contract id you are given, and no others.")
	return b.String()
}

// Prompt is the user message for one request.
func Prompt(items []Item) (string, error) {
	data, err := json.MarshalIndent(items, "", " ")
	if err != nil {
		return "", err
	}
	return "Classify these contracts:\n\n" + string(data), nil
}

// Schema is the structured-output JSON schema for a response.
func Schema() map[string]any {
	ids := make([]any, len(Categories))
	for i, c := range Categories {
		ids[i] = c.ID
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"results"},
		"properties": map[string]any{
			"results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"id", "category", "confidence"},
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"category":   map[string]any{"type": "string", "enum": ids},
						"confidence": map[string]any{"type": "string", "enum": []any{"high", "medium", "low"}},
					},
				},
			},
		},
	}
}

// Parse reads a response's JSON text and keeps only results for contracts
// that were in the request, with a known category.
func Parse(text string, sent []Item) ([]Result, error) {
	var out struct {
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	want := map[string]bool{}
	for _, it := range sent {
		want[it.CNID] = true
	}
	known := map[string]bool{}
	for _, c := range Categories {
		known[c.ID] = true
	}
	var kept []Result
	for _, r := range out.Results {
		if want[r.CNID] && known[r.Category] {
			kept = append(kept, r)
			delete(want, r.CNID) // first answer per id wins
		}
	}
	return kept, nil
}

// InputHash fingerprints what the model saw for one contract, so a changed
// description is reclassified.
func InputHash(it Item) string {
	h := sha256.Sum256([]byte(it.Description + "\x00" + it.Agency + "\x00" + it.Supplier))
	return hex.EncodeToString(h[:12])
}

// LabelOf returns the display label for a category ID.
func LabelOf(id string) string {
	for _, c := range Categories {
		if c.ID == id {
			return c.Label
		}
	}
	return id
}
