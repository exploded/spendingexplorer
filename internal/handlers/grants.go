package handlers

import (
	"context"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

// grantsSection is an entity's GrantConnect grant awards active in the year,
// with each award's value spread over its term.
type grantsSection struct {
	Stats      db.EntityGrantStatsRow
	Categories []grantCategory
	Recipients []db.EntityGrantRecipientsRow
}

type grantCategory struct {
	db.EntityGrantCategoriesRow
	Share float64
}

func (h *Handler) entityGrants(ctx context.Context, entityID int64, fy string) (*grantsSection, error) {
	stats, err := h.q.EntityGrantStats(ctx, db.EntityGrantStatsParams{Fy: fy, EntityID: sqlNullInt(entityID)})
	if err != nil || stats.Grants == 0 {
		return nil, err
	}
	cats, err := h.q.EntityGrantCategories(ctx, db.EntityGrantCategoriesParams{Fy: fy, EntityID: sqlNullInt(entityID), Lim: 8})
	if err != nil {
		return nil, err
	}
	rec, err := h.q.EntityGrantRecipients(ctx, db.EntityGrantRecipientsParams{Fy: fy, EntityID: sqlNullInt(entityID), Lim: 15})
	if err != nil {
		return nil, err
	}
	sec := &grantsSection{Stats: stats, Recipients: rec}
	for _, c := range cats {
		sec.Categories = append(sec.Categories, grantCategory{c, explore.Share(c.Amount, stats.Amount)})
	}
	return sec, nil
}
