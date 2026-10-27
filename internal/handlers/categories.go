package handlers

import (
	"context"

	"spendingexplorer/internal/classify"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

// categorySection breaks an entity's professional-services contracts
// (UNSPSC segment 80) down by the kind of work an AI model read in each
// description. Low-confidence results are kept out of the categories.
type categorySection struct {
	FY           string
	Rows         []categoryRow
	Total        int64 // confident results only
	Low          int64
	LowContracts int64
	Model        string
	Prompt       string
	Compare      *consultancyCompare
}

type categoryRow struct {
	Label     string
	Contracts int64
	Amount    int64
	Share     float64
}

// consultancyCompare sets the consultancy an entity reported for a year
// beside the contracts classified as consultancy for the same year.
type consultancyCompare struct {
	Year       string
	Reported   int64
	Classified int64
}

func (h *Handler) entityCategories(ctx context.Context, entityID int64, fy string, reported *reportedSection) (*categorySection, error) {
	rows, err := h.categoryTotals(ctx, entityID, fy)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	sec := &categorySection{FY: fy, Model: classify.Model, Prompt: classify.PromptVersion}
	for _, r := range rows {
		if r.Low == 1 {
			sec.Low += r.Amount
			sec.LowContracts += r.Contracts
			continue
		}
		sec.Total += r.Amount
		sec.Rows = append(sec.Rows, categoryRow{Label: classify.LabelOf(r.Category), Contracts: r.Contracts, Amount: r.Amount})
	}
	for i := range sec.Rows {
		sec.Rows[i].Share = explore.Share(sec.Rows[i].Amount, sec.Total)
	}

	// Compare with the latest consultancy figure the entity reported that
	// passed its checks.
	if reported != nil {
		for _, y := range reported.Years {
			if y.Consultancy == nil || y.Consultancy.Note != 0 {
				continue
			}
			then, err := h.categoryTotals(ctx, entityID, y.Year)
			if err != nil {
				return nil, err
			}
			c := &consultancyCompare{Year: y.Year, Reported: y.Consultancy.Amount}
			for _, r := range then {
				if r.Category == "consultancy" && r.Low == 0 {
					c.Classified += r.Amount
				}
			}
			sec.Compare = c
			break
		}
	}
	return sec, nil
}

func (h *Handler) categoryTotals(ctx context.Context, entityID int64, fy string) ([]db.EntityCategoryTotalsRow, error) {
	return h.q.EntityCategoryTotals(ctx, db.EntityCategoryTotalsParams{
		Task: classify.Task, PromptVersion: classify.PromptVersion, Fy: fy, EntityID: sqlNullInt(entityID),
	})
}
