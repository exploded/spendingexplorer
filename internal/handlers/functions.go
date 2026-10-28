package handlers

import (
	"context"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

// functionRow is one BP1 function on the home page, with its sub-functions
// from Budget Paper No. 1 Appendix A.
type functionRow struct {
	row
	Subs *subList // nil when the function has no sub-functions
	Open bool
}

// subList is one level of rows inside Of, for the "subfns" template.
type subList struct {
	Of   string
	Prev string // previous fiscal year, for the tooltip
	Rows []subRow
}

// subRow is a sub-function, or a row that itemises one. Share is of its parent.
type subRow struct {
	row
	Meter     float64 // Share clamped to 0–100 for the bar
	Source    string  // set when the row comes from a different document than its parent
	SourceURL string
	Table     string
	Gap       bool // the part of the parent its itemised rows don't cover
	Children  *subList
}

// openFunction is expanded by default: its name says nothing about what is in it.
const openFunction = "Other purposes"

// functionRows attaches sub-functions, and any rows itemising them, to the BP1
// functions. Where a sub-function is itemised from another document and the
// rows don't add up, the difference is shown as its own labelled row.
func (h *Handler) functionRows(ctx context.Context, p period, funcs []row) ([]functionRow, error) {
	subs, err := h.q.SubfunctionTotals(ctx, db.SubfunctionTotalsParams{Fy: p.FY, PrevFy: p.Prev, BudgetYear: p.FY})
	if err != nil {
		return nil, err
	}
	top := map[string][]subRow{}      // by function
	children := map[string][]subRow{} // by function and parent
	for _, s := range subs {
		r := subRow{
			row:       row{Node: explore.Node{Name: s.Name, Cur: s.Cur, Prev: s.Prev, Note: s.Note}},
			Source:    shortSource(s.SourceTitle),
			SourceURL: s.SourceUrl,
			Table:     s.SourceTable,
		}
		if s.Parent == "" {
			top[s.FunctionName] = append(top[s.FunctionName], r)
		} else {
			children[s.FunctionName+"|"+s.Parent] = append(children[s.FunctionName+"|"+s.Parent], r)
		}
	}

	out := make([]functionRow, 0, len(funcs))
	for _, f := range funcs {
		fr := functionRow{row: f, Open: f.Name == openFunction}
		if rows := top[f.Name]; len(rows) > 0 {
			fr.Subs = &subList{Of: f.Name, Prev: p.Prev}
			for _, s := range rows {
				s.setShare(f.Cur)
				if kids := itemise(s, children[f.Name+"|"+s.Name]); kids != nil {
					s.Children = &subList{Of: s.Name, Prev: p.Prev, Rows: kids}
				}
				s.Source, s.SourceURL = "", "" // a sub-function shares its function's source
				fr.Subs.Rows = append(fr.Subs.Rows, s)
			}
		}
		out = append(out, fr)
	}
	return out, nil
}

func (s *subRow) setShare(whole int64) {
	s.Share = explore.Share(s.Cur, whole)
	s.Meter = min(max(s.Share, 0), 100)
}

// itemise sets each child's share of parent and, when the children come from
// another document and fall short of the parent by more than rounding, adds
// the rest as a gap row.
func itemise(parent subRow, kids []subRow) []subRow {
	if len(kids) == 0 {
		return nil
	}
	var cur, prev int64
	for i := range kids {
		kids[i].setShare(parent.Cur)
		cur += kids[i].Cur
		prev += kids[i].Prev
		if kids[i].Source == parent.Source {
			kids[i].Source, kids[i].SourceURL = "", ""
		}
	}
	src := kids[0].Source
	if src == "" {
		return kids // same document: the loader checked they add up
	}
	gap := subRow{Gap: true, row: row{Node: explore.Node{
		Name: "Not itemised in " + src, Cur: parent.Cur - cur, Prev: parent.Prev - prev,
		Note: "The difference between this sub-function in " + parent.Source + " and the amounts " + src + " lists for it.",
	}}}
	tolerance := int64(len(kids)) * 1_000_000
	if abs64(gap.Cur) > tolerance || abs64(gap.Prev) > tolerance {
		gap.setShare(parent.Cur)
		kids = append(kids, gap)
	}
	return kids
}

// shortSource turns "2026-27 Budget Paper No. 3 – Federal Financial Relations"
// into "Budget Paper No. 3".
func shortSource(title string) string {
	t, _, _ := strings.Cut(title, " – ")
	if y, rest, ok := strings.Cut(t, " "); ok && len(y) == 7 && y[4] == '-' {
		t = rest
	}
	return t
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
