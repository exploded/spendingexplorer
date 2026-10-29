package website

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	Task          = "find_website"
	PromptVersion = "v1"
	Model         = "claude-haiku-5-5"
	// MaxSearches caps web searches per supplier; each costs US$0.01.
	MaxSearches = 3
)

// System is the instruction sent with every search.
func System() string {
	return `You find the official website of an organisation that holds Australian Government contracts.

Use web search. Give the organisation's own website: its home page, or its Australian page on a global group's site. If the organisation has no site of its own but trades as part of a group under the same name, give the group's site.

Never give a directory, business register, social media page, news article, job board, government tender page or a different organisation with a similar name. Use the ABN, location and contract details to tell organisations of the same name apart. If you are not confident, answer none: no answer is better than a wrong one.

End your reply with one line in exactly this form, and nothing after it:
WEBSITE: https://example.com.au/
or
WEBSITE: none`
}

// Context is what the model is told about a supplier besides its name.
type Context struct {
	Agency      string // the agency it has the most contract value with
	Description string // its largest contract
}

// Prompt describes one supplier.
func Prompt(s Supplier, c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Organisation: %s\n", s.Name)
	if s.ABN != "" {
		fmt.Fprintf(&b, "ABN: %s\n", s.ABN)
	}
	loc := s.Country
	if s.Postcode != "" {
		loc = strings.TrimSpace("postcode " + s.Postcode + ", " + s.Country)
	}
	if loc != "" {
		fmt.Fprintf(&b, "Location: %s\n", loc)
	}
	if c.Agency != "" {
		fmt.Fprintf(&b, "Main government customer: %s\n", c.Agency)
	}
	if c.Description != "" {
		fmt.Fprintf(&b, "Largest contract: %s\n", c.Description)
	}
	return b.String()
}

var (
	answer  = regexp.MustCompile(`(?im)^\W*WEBSITE:[\s*_]*([^\s*_]\S*)`)
	bareURL = regexp.MustCompile(`(?i)\n[\s*_<]*(https?://\S+?)[\s*_>]*$`)
)

// Parse reads the model's answer. It returns "" for none.
func Parse(text string) (string, error) {
	var u string
	if m := answer.FindAllStringSubmatch(text, -1); m != nil {
		u = m[len(m)-1][1]
	} else if m := bareURL.FindStringSubmatch(text); m != nil {
		// The model sometimes ends with the address alone.
		u = m[1]
	} else {
		return "", fmt.Errorf("no WEBSITE line in %q", tail(text))
	}
	u = strings.Trim(u, "<>()[]*`'\".,")
	if strings.EqualFold(u, "none") {
		return "", nil
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	return u, nil
}

func tail(s string) string {
	if len(s) > 200 {
		return "…" + s[len(s)-200:]
	}
	return s
}
