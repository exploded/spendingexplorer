// Package pbsdoc extracts program objectives from Portfolio Budget Statements
// documents. The line-item tables carry only program names; the PBS documents
// describe each program in Section 2, under its heading and before its "Key
// Activity" performance rows. Text is quoted as published, never rewritten.
package pbsdoc

import (
	"regexp"
	"strings"
	"unicode"

	"spendingexplorer/internal/pbs"
)

// Objective is one program's objective text and where it was found.
type Objective struct {
	Code     string // e.g. 2.1
	Heading  string // e.g. Program 2.1: Medical Benefits
	CodeOnly bool   // the heading gave a number but no name ("Program 2.2" / "Objective")
	Text     string
	Page     int // 1-based page in the source document
}

var (
	heading = regexp.MustCompile(`^\s*Program\s+(\d+(?:\.\d+)*)\s*[:–-]?\s*(\S.*?)\s*$`)
	// Lines that end an objective block.
	stop = regexp.MustCompile(`(?i)^\s*(key activit|year\s|current year|budget year|performance measure|program\s+\d|programs?\s*$|outcome\s+\d|table\s+\d|linked program|contribution to|budgeted expenses|\d+\s*$)`)
	// Text about another entity's program, from "linked programs" sections:
	// it names the label, describes an entity in the third person, or cites
	// program numbers in brackets such as "(1.1)".
	linked = regexp.MustCompile(`(?i)linked program|contribution to outcome|contributes to outcome|^the [^.]{0,90}\b(has (strategic |lead |overall )?(policy )?responsibility|is responsible for|contributes to|works with)|\(\d+\.\d+(\s*(,|and)\s*\d+\.\d+)*\)`)
	label  = regexp.MustCompile(`(?i)^objectives?:?$`)
	// Signs a block is a financial table rather than prose.
	tabular = regexp.MustCompile(`\$'000|\d{1,3},\d{3}`)
)

// Extract finds program objectives in text produced by `pdftotext -layout`,
// with pages separated by form feeds. When a heading appears more than once,
// the block followed by "Key Activity" wins, then the longest.
func Extract(text string) []Objective {
	pages := strings.Split(text, "\f")
	best := map[string]Objective{}
	bestKeyed := map[string]bool{}
	var order []string

	for p, page := range pages {
		lines := strings.Split(page, "\n")
		for i := 0; i < len(lines); i++ {
			m := heading.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			name := strings.TrimSpace(m[2])
			codeOnly := label.MatchString(name)
			var block []string
			keyed := false
			for j := i + 1; j < len(lines) && j < i+16; j++ {
				l := strings.TrimSpace(lines[j])
				if len(block) == 0 && l != "" {
					first := []rune(l)[0]
					switch {
					case label.MatchString(l):
						codeOnly = codeOnly || name == ""
						continue
					case unicode.IsLower(first) && !codeOnly:
						// The heading wrapped onto this line.
						name += " " + l
						continue
					}
				}
				if stop.MatchString(l) {
					keyed = strings.HasPrefix(strings.ToLower(l), "key activit")
					break
				}
				if l == "" && len(block) > 0 && strings.HasSuffix(block[len(block)-1], ".") {
					// A blank line after a full sentence may end the paragraph;
					// keep going only if prose continues.
					continue
				}
				if l != "" {
					block = append(block, l)
				}
			}
			text := pbs.CleanText(strings.Join(block, " "))
			if !prose(text) || linked.MatchString(text) {
				continue
			}
			code := m[1]
			o := Objective{Code: code, Heading: "Program " + code + ": " + pbs.CleanText(name), CodeOnly: codeOnly, Text: text, Page: p + 1}
			if codeOnly {
				o.Heading = "Program " + code
			}
			prev, seen := best[code]
			switch {
			case !seen:
				order = append(order, code)
				best[code], bestKeyed[code] = o, keyed
			case keyed && !bestKeyed[code]:
				best[code], bestKeyed[code] = o, true
			case keyed == bestKeyed[code] && len(o.Text) > len(prev.Text):
				best[code] = o
			}
		}
	}
	out := make([]Objective, 0, len(order))
	for _, c := range order {
		out = append(out, best[c])
	}
	return out
}

// prose rejects empty blocks, financial tables and bare lists of headings.
func prose(s string) bool {
	if len(s) < 60 || len(s) > 1200 || tabular.MatchString(s) {
		return false
	}
	return strings.Count(s, " ") >= 8
}
