package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
	"spendingexplorer/internal/grantconnect"
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

// recipientURL is the page of the grant recipient with this key.
func recipientURL(key string) string {
	return "/recipient/" + url.PathEscape(grantconnect.RecipientSlug(key))
}

// recipientAwardLimit is how many awards a recipient page lists.
const recipientAwardLimit = 100

type recipientPage struct {
	Period   period
	Crumbs   []crumb
	Name     string
	Totals   db.RecipientTotalsRow
	Year     db.RecipientYearRow
	Agencies []db.RecipientAgenciesRow
	Awards   []db.RecipientAwardsRow
	Supplier *db.RecipientSupplierRow // matched by name, if any
}

func (h *Handler) recipient(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := grantconnect.KeyFromSlug(r.PathValue("key"))
	if key == "" {
		http.NotFound(w, r)
		return
	}
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	name, err := h.q.RecipientName(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	page := recipientPage{Period: p, Name: name, Crumbs: []crumb{{"Whole of government", "/"}}}
	if page.Totals, err = h.q.RecipientTotals(ctx, key); err != nil {
		h.fail(w, err)
		return
	}
	if page.Year, err = h.q.RecipientYear(ctx, db.RecipientYearParams{Fy: p.FY, RecipientKey: key}); err != nil {
		h.fail(w, err)
		return
	}
	if page.Agencies, err = h.q.RecipientAgencies(ctx, key); err != nil {
		h.fail(w, err)
		return
	}
	if page.Awards, err = h.q.RecipientAwards(ctx, db.RecipientAwardsParams{RecipientKey: key, Limit: recipientAwardLimit}); err != nil {
		h.fail(w, err)
		return
	}
	if s, err := h.q.RecipientSupplier(ctx, key); err == nil {
		page.Supplier = &s
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.fail(w, err)
		return
	}
	h.render(w, "recipient.html", page)
}

// goPattern is a GrantConnect grant opportunity ID, such as GO5643.
var goPattern = regexp.MustCompile(`^GO\d+$`)

type roundPage struct {
	Period     period
	Crumbs     []crumb
	GoID       string
	Totals     db.RoundTotalsRow
	Year       db.RoundYearRow
	Activities []db.RoundActivitiesRow // most common first
	Agencies   []db.RoundAgenciesRow
	Recipients []db.RoundRecipientsRow
}

func (h *Handler) round(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	goID := r.PathValue("go")
	if !goPattern.MatchString(goID) {
		http.NotFound(w, r)
		return
	}
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := roundPage{Period: p, GoID: goID, Crumbs: []crumb{{"Whole of government", "/"}}}
	if page.Totals, err = h.q.RoundTotals(ctx, goID); err != nil {
		h.fail(w, err)
		return
	}
	if page.Totals.Awards == 0 {
		http.NotFound(w, r)
		return
	}
	if page.Year, err = h.q.RoundYear(ctx, db.RoundYearParams{Fy: p.FY, GoID: goID}); err != nil {
		h.fail(w, err)
		return
	}
	if page.Activities, err = h.q.RoundActivities(ctx, db.RoundActivitiesParams{GoID: goID, Limit: 5}); err != nil {
		h.fail(w, err)
		return
	}
	if page.Agencies, err = h.q.RoundAgencies(ctx, goID); err != nil {
		h.fail(w, err)
		return
	}
	if page.Recipients, err = h.q.RoundRecipients(ctx, db.RoundRecipientsParams{GoID: goID, Lim: 50}); err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, "round.html", page)
}
