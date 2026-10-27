package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"

	"spendingexplorer/internal/db"
)

// investmentRow is one item of the net cash flows from investments in
// financial assets for policy purposes. Amounts are net paid out (the
// published sign reversed), so a negative amount is net received.
type investmentRow struct {
	Name       string
	PrevActual int64 // previous year: outcome if published, else estimated actual
	Cur        int64 // current year, Budget estimate
	Earlier    int64 // current year as estimated in the previous Budget
	HasEarlier bool
	Forward    int64 // current year to the last forward estimate
	Other      bool  // Treasury's "Net other": not itemised
	Key        string
	Projects   []projectView // major projects this item funds
}

type investmentsPage struct {
	Period   period
	Rows     []investmentRow
	Total    investmentRow
	Itemised int64 // current year, excluding "Net other"
	LastYear string
	Expenses int64 // total expenses in the current year, for scale
	Budget   db.InvestmentDocumentRow
	Earlier  *db.InvestmentDocumentRow // previous Budget, for revisions
	Outcome  *db.InvestmentDocumentRow // previous year's Final Budget Outcome
	Projects []projectView
}

func (h *Handler) investments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := investmentsPage{Period: p}
	budget, ok, err := h.investmentDoc(ctx, "bp1_investments", p.FY)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	page.Budget = budget
	rows := map[string]*investmentRow{}
	row := func(key, name string) *investmentRow {
		if key == "total" {
			return &page.Total
		}
		if rows[key] == nil {
			rows[key] = &investmentRow{Name: name, Key: key, Other: key == "net other"}
		}
		return rows[key]
	}
	// existing finds an item already listed in the current Budget.
	existing := func(key string) *investmentRow {
		if key == "total" {
			return &page.Total
		}
		return rows[key]
	}

	amounts, err := h.q.PolicyInvestments(ctx, budget.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	for _, a := range amounts {
		ir, out := row(a.ItemKey, a.Item), -a.Amount
		switch {
		case a.FiscalYear == p.Prev:
			ir.PrevActual = out
		case a.FiscalYear == p.FY:
			ir.Cur = out
		}
		if a.FiscalYear >= p.FY {
			ir.Forward += out
			page.LastYear = max(page.LastYear, a.FiscalYear)
		}
	}

	// The previous year's outcome replaces its estimated actual. Items it does
	// not list keep the estimate, and so cannot be mistaken for zero.
	if doc, ok, err := h.investmentDoc(ctx, "fbo_investments", p.Prev); err != nil {
		h.fail(w, err)
		return
	} else if ok {
		page.Outcome = &doc
		amounts, err := h.q.PolicyInvestments(ctx, doc.ID)
		if err != nil {
			h.fail(w, err)
			return
		}
		for _, a := range amounts {
			if ir := existing(a.ItemKey); ir != nil {
				ir.PrevActual = -a.Amount
			}
		}
	}

	if doc, ok, err := h.investmentDoc(ctx, "bp1_investments", p.Prev); err != nil {
		h.fail(w, err)
		return
	} else if ok {
		page.Earlier = &doc
		amounts, err := h.q.PolicyInvestments(ctx, doc.ID)
		if err != nil {
			h.fail(w, err)
			return
		}
		for _, a := range amounts {
			if ir := existing(a.ItemKey); ir != nil && a.FiscalYear == p.FY {
				ir.Earlier, ir.HasEarlier = -a.Amount, true
			}
		}
	}

	if page.Projects, err = h.projects(ctx); err != nil {
		h.fail(w, err)
		return
	}
	links := projectLinks(page.Projects)
	for _, ir := range rows {
		ir.Projects = links[ir.Key]
		page.Rows = append(page.Rows, *ir)
		if !ir.Other {
			page.Itemised += ir.Cur
		}
	}
	sort.Slice(page.Rows, func(i, j int) bool {
		a, b := page.Rows[i], page.Rows[j]
		if a.Other != b.Other {
			return b.Other
		}
		return a.Cur > b.Cur
	})

	_, total, _, err := h.reconcile(ctx, p)
	if err != nil {
		h.fail(w, err)
		return
	}
	page.Expenses = total.Cur
	h.render(w, "investments.html", page)
}

// investmentDoc returns the investments table published in a given year's
// Budget (bp1_investments) or Final Budget Outcome (fbo_investments).
func (h *Handler) investmentDoc(ctx context.Context, docType, year string) (db.InvestmentDocumentRow, bool, error) {
	doc, err := h.q.InvestmentDocument(ctx, db.InvestmentDocumentParams{DocType: docType, BudgetYear: year})
	if errors.Is(err, sql.ErrNoRows) {
		return doc, false, nil
	}
	return doc, err == nil, err
}

// investmentTotal is the net amount paid out as loans and investments in a
// year, from that year's Budget, or 0 if it is not loaded.
func (h *Handler) investmentTotal(ctx context.Context, fy string) (int64, error) {
	doc, ok, err := h.investmentDoc(ctx, "bp1_investments", fy)
	if err != nil || !ok {
		return 0, err
	}
	amounts, err := h.q.PolicyInvestments(ctx, doc.ID)
	if err != nil {
		return 0, err
	}
	for _, a := range amounts {
		if a.IsTotal == 1 && a.FiscalYear == fy {
			return -a.Amount, nil
		}
	}
	return 0, nil
}
