package handlers

import (
	"context"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

// outcomeRow compares one function's Budget estimate with its actual. Node.Prev
// is the Budget estimate and Node.Cur the actual, so Change is actual less
// budget.
type outcomeRow struct {
	explore.Node
	Revised    int64 // the year as estimated in the next Budget
	HasRevised bool
}

// outcomeSection is the most recent year with a Final Budget Outcome, by
// function: what that year's Budget estimated and what was spent.
type outcomeSection struct {
	FY      string
	Rows    []outcomeRow
	Total   outcomeRow
	Budget  db.OutcomeDocumentsRow
	Revised *db.OutcomeDocumentsRow
	Actual  db.OutcomeDocumentsRow
}

// outcome returns nil until both the year's Budget and its Final Budget
// Outcome are loaded.
func (h *Handler) outcome(ctx context.Context) (*outcomeSection, error) {
	fy, err := h.q.LatestOutcomeYear(ctx)
	if err != nil || fy == "" {
		return nil, err
	}
	s := &outcomeSection{FY: fy}
	docs, err := h.q.OutcomeDocuments(ctx, fy)
	if err != nil {
		return nil, err
	}
	var hasBudget bool
	for _, d := range docs {
		switch d.EstimateType {
		case "budget":
			s.Budget, hasBudget = d, true
		case "estimated_actual":
			s.Revised = &d
		case "actual":
			s.Actual = d
		}
	}
	if !hasBudget {
		return nil, nil
	}
	fns, err := h.q.FunctionOutcomes(ctx, fy)
	if err != nil {
		return nil, err
	}
	for _, f := range fns {
		// A function missing from either publication cannot be compared.
		if f.HasBudget == 0 || f.HasActual == 0 {
			continue
		}
		r := outcomeRow{
			Node:    explore.Node{Name: f.Category, Cur: f.Actual, Prev: f.Budget},
			Revised: f.Revised, HasRevised: f.HasRevised > 0,
		}
		if f.Category == "Total expenses" {
			s.Total = r
			continue
		}
		s.Rows = append(s.Rows, r)
	}
	if s.Total.Name == "" {
		return nil, nil
	}
	return s, nil
}
