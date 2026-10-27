package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

type projectsPage struct {
	Period   period
	Crumbs   []crumb
	Projects []projectView
}

func (h *Handler) projectsIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := projectsPage{Period: p, Crumbs: []crumb{{"Whole of government", "/"}}}
	if page.Projects, err = h.projects(ctx); err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, "projects.html", page)
}

// fundingLine is an investment line that funds a project, with its net
// amount paid out in the current Budget year.
type fundingLine struct {
	Name  string
	Cur   int64
	Other bool // Treasury's "Net other", which is not itemised
}

type projectPage struct {
	Period    period
	Crumbs    []crumb
	Project   projectView
	Funding   []fundingLine
	Entities  []db.ProjectLinksRow
	Suppliers []db.ProjectLinksRow
}

func (h *Handler) project(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	all, err := h.projects(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := projectPage{Period: p, Crumbs: []crumb{{"Whole of government", "/"}, {"Major projects", "/projects"}}}
	found := false
	for _, pv := range all {
		if pv.Key == r.PathValue("key") {
			page.Project, found = pv, true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	links, err := h.q.ProjectLinks(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	for _, l := range links {
		switch {
		case l.ProjectKey != page.Project.Key:
		case l.EntityID != 0:
			page.Entities = append(page.Entities, l)
		default:
			page.Suppliers = append(page.Suppliers, l)
		}
	}

	if doc, ok, err := h.investmentDoc(ctx, "bp1_investments", p.FY); err != nil {
		h.fail(w, err)
		return
	} else if ok {
		amounts, err := h.q.PolicyInvestments(ctx, doc.ID)
		if err != nil {
			h.fail(w, err)
			return
		}
		keys := map[string]bool{}
		for _, k := range strings.Split(page.Project.ItemKeys, "|") {
			keys[k] = true
		}
		for _, a := range amounts {
			if keys[a.ItemKey] && a.FiscalYear == p.FY {
				page.Funding = append(page.Funding, fundingLine{Name: a.Item, Cur: -a.Amount, Other: a.ItemKey == "net other"})
			}
		}
	}
	h.render(w, "project.html", page)
}

// linkedProjects returns the projects linked to an entity or a supplier;
// pass 0 for the other.
func (h *Handler) linkedProjects(ctx context.Context, entityID, supplierID int64) ([]projectView, error) {
	links, err := h.q.ProjectLinks(ctx)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, l := range links {
		if (entityID != 0 && l.EntityID == entityID) || (supplierID != 0 && l.SupplierID == supplierID) {
			keys[l.ProjectKey] = true
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	all, err := h.projects(ctx)
	if err != nil {
		return nil, err
	}
	var out []projectView
	for _, pv := range all {
		if keys[pv.Key] {
			out = append(out, pv)
		}
	}
	return out, nil
}

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
