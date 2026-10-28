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
	Keyed    bool   // the block ends at "Key Activity", the surest sign of an objective
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
	// A "Linked programs" section lists other entities' programs under their
	// own headings, each followed by how that entity contributes to this one's
	// outcome. It runs until the outcome's budgeted expenses table.
	linkedStart = regexp.MustCompile(`(?i)^\s*(linked programs?(\s+for outcome \d+)?\d?|other commonwealth entities that contribute to outcome \d+)\s*(\(continued\))?\s*$`)
	linkedEnd   = regexp.MustCompile(`(?i)^\s*(budgeted expenses|table \d)`)
	// A heading followed by a column gap and figures is a financial table row.
	tableRow = regexp.MustCompile(`\S\s{3,}[(\d][\d,.()]*\s*$`)
	label       = regexp.MustCompile(`(?i)^objectives?:?$`)
	// Signs a block is a financial table rather than prose.
	tabular = regexp.MustCompile(`\$'000|\d{1,3},\d{3}`)
)

// Extract finds program objectives in text produced by `pdftotext -layout`,
// with pages separated by form feeds. Headings inside "Linked programs"
// sections belong to other entities and are skipped. A portfolio document
// covers several entities, each with its own Program 1.1, so a heading is
// identified by number and name; when one appears more than once, the block
// followed by "Key Activity" wins, then the longest.
func Extract(text string) []Objective {
	pages := strings.Split(text, "\f")
	best := map[string]Objective{}
	var order []string
	inLinked := false

	for p, page := range pages {
		lines := strings.Split(page, "\n")
		for i := 0; i < len(lines); i++ {
			switch {
			case linkedStart.MatchString(lines[i]):
				inLinked = true
			case linkedEnd.MatchString(lines[i]):
				inLinked = false
			}
			m := heading.FindStringSubmatch(lines[i])
			if m == nil || inLinked || tableRow.MatchString(lines[i]) {
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
			o := Objective{Code: code, Heading: "Program " + code + ": " + pbs.CleanText(name), CodeOnly: codeOnly, Keyed: keyed, Text: text, Page: p + 1}
			if codeOnly {
				o.Heading = "Program " + code
			}
			key := strings.ToLower(o.Heading)
			prev, seen := best[key]
			if !seen {
				order = append(order, key)
			}
			if !seen || Better(o, prev) {
				best[key] = o
			}
		}
	}
	out := make([]Objective, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

// Better reports whether a is a better quote than b for the same program: a
// block followed by "Key Activity" wins, then the longer text.
func Better(a, b Objective) bool {
	if a.Keyed != b.Keyed {
		return a.Keyed
	}
	return len(a.Text) > len(b.Text)
}

// prose rejects empty blocks, financial tables and bare lists of headings.
func prose(s string) bool {
	if len(s) < 60 || len(s) > 1200 || tabular.MatchString(s) {
		return false
	}
	return strings.Count(s, " ") >= 8
}
