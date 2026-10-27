package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

// projectView is a project with its published cost estimates, oldest first.
type projectView struct {
	db.ListProjectsRow
	Estimates    []estimateView
	First        estimateView
	Latest       estimateView
	Multiple     string // latest estimate as a multiple of the first, e.g. "6.0×"
	ScopeChanged bool   // scope changed at some point after the first estimate
}

type estimateView struct {
	db.ListProjectEstimatesRow
	Amount string // "$12.0bn", or "$46bn–$56bn" for a range
	Date   string // "Mar 2017"
}

// projects loads every project and its estimates.
func (h *Handler) projects(ctx context.Context) ([]projectView, error) {
	ps, err := h.q.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	es, err := h.q.ListProjectEstimates(ctx)
	if err != nil {
		return nil, err
	}
	byKey := map[string][]estimateView{}
	for _, e := range es {
		byKey[e.ProjectKey] = append(byKey[e.ProjectKey], estimateView{ListProjectEstimatesRow: e, Amount: estimateAmount(e), Date: monthYear(e.Published)})
	}
	var out []projectView
	for _, p := range ps {
		v := projectView{ListProjectsRow: p, Estimates: byKey[p.Key]}
		if len(v.Estimates) == 0 {
			continue
		}
		v.First, v.Latest = v.Estimates[0], v.Estimates[len(v.Estimates)-1]
		for _, e := range v.Estimates[1:] {
			v.ScopeChanged = v.ScopeChanged || e.ScopeChanged == 1
		}
		if len(v.Estimates) > 1 {
			v.Multiple = multiple(v.First.ListProjectEstimatesRow, v.Latest.ListProjectEstimatesRow)
		}
		out = append(out, v)
	}
	return out, nil
}

func estimateAmount(e db.ListProjectEstimatesRow) string {
	if e.AmountHigh.Valid {
		return explore.Money(e.Amount) + "–" + explore.Money(e.AmountHigh.Int64)
	}
	return explore.Money(e.Amount)
}

// multiple gives the latest estimate as a multiple of the first. Ranges give
// the widest possible span.
func multiple(first, latest db.ListProjectEstimatesRow) string {
	high := func(e db.ListProjectEstimatesRow) float64 {
		if e.AmountHigh.Valid {
			return float64(e.AmountHigh.Int64)
		}
		return float64(e.Amount)
	}
	lo, hi := float64(latest.Amount)/high(first), high(latest)/float64(first.Amount)
	if fmt.Sprintf("%.1f", lo) == fmt.Sprintf("%.1f", hi) {
		return fmt.Sprintf("%.1f×", lo)
	}
	return fmt.Sprintf("%.1f–%.1f×", lo, hi)
}

// monthYear turns "2017-03" or "2017-03-16" into "Mar 2017".
func monthYear(d string) string {
	t, err := time.Parse("2006-01", d[:min(len(d), 7)])
	if err != nil {
		return d
	}
	return t.Format("Jan 2006")
}

// projectLinks maps each investment item key to the projects it funds.
func projectLinks(ps []projectView) map[string][]projectView {
	m := map[string][]projectView{}
	for _, p := range ps {
		for _, k := range strings.Split(p.ItemKeys, "|") {
			if k != "" {
				m[k] = append(m[k], p)
			}
		}
	}
	return m
}
