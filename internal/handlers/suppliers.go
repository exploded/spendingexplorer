package handlers

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
)

// rankLimit is how many rows the supplier ranking lists.
const rankLimit = 100

// rankRow is one row of the supplier ranking: a supplier, or a corporate
// group when IsGroup is set.
type rankRow struct {
	ID            int64
	IsGroup       bool
	Name          string
	NameBasis     string
	Suppliers     int64
	Contracts     int64
	Amount        int64
	LimitedAmount int64
	Entities      int64
	Share         float64 // of all contract value in the year
}

type suppliersPage struct {
	Period   period
	Grouped  bool
	Total    db.ContractCoverageTotalRow
	Groups   db.SupplierGroupStatsRow
	Rows     []rankRow
	Query    string
	Searched bool
	Results  []db.SearchSuppliersRow
	Crumbs   []crumb
}

func (h *Handler) suppliers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := suppliersPage{Period: p, Grouped: r.URL.Query().Get("view") != "abn", Query: strings.TrimSpace(r.URL.Query().Get("q")),
		Crumbs: []crumb{{"Whole of government", "/"}}}
	if len([]rune(page.Query)) >= 2 {
		page.Searched = true
		if page.Results, err = h.searchSuppliers(r, page.Query, 50); err != nil {
			h.fail(w, err)
			return
		}
	}
	// The search box asks for its results only.
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "search-results" {
		h.renderBlock(w, "suppliers.html", "search-results", page)
		return
	}

	if page.Total, err = h.q.ContractCoverageTotal(ctx, p.FY); err != nil {
		h.fail(w, err)
		return
	}
	if page.Groups, err = h.q.SupplierGroupStats(ctx); err != nil {
		h.fail(w, err)
		return
	}
	if page.Grouped {
		rows, err := h.q.TopSupplierGroups(ctx, db.TopSupplierGroupsParams{Fy: p.FY, Lim: rankLimit})
		if err != nil {
			h.fail(w, err)
			return
		}
		for _, s := range rows {
			page.Rows = append(page.Rows, rankRow{ID: s.ID, IsGroup: s.IsGroup == 1, Name: s.Name, NameBasis: s.NameBasis, Suppliers: s.Suppliers,
				Contracts: s.Contracts, Amount: s.Amount, LimitedAmount: s.LimitedAmount, Entities: s.Entities})
		}
	} else {
		rows, err := h.q.TopSuppliers(ctx, db.TopSuppliersParams{Fy: p.FY, Lim: rankLimit})
		if err != nil {
			h.fail(w, err)
			return
		}
		for _, s := range rows {
			page.Rows = append(page.Rows, rankRow{ID: s.ID, Name: s.Name, Suppliers: 1,
				Contracts: s.Contracts, Amount: s.Amount, LimitedAmount: s.LimitedAmount, Entities: s.Entities})
		}
	}
	for i := range page.Rows {
		page.Rows[i].Share = explore.Share(page.Rows[i].Amount, page.Total.Amount)
	}
	h.render(w, "suppliers.html", page)
}

// searchSuppliers matches a name fragment, or an ABN typed with or without
// spaces.
func (h *Handler) searchSuppliers(r *http.Request, query string, limit int64) ([]db.SearchSuppliersRow, error) {
	abn := strings.Join(strings.Fields(query), "")
	if _, err := strconv.ParseUint(abn, 10, 64); err != nil || len(abn) != 11 {
		abn = ""
	}
	return h.q.SearchSuppliers(r.Context(), db.SearchSuppliersParams{Fragment: query, Abn: abn, Lim: limit})
}

type groupPage struct {
	Period    period
	Group     db.GetSupplierGroupRow
	Members   []db.SupplierGroupMembersRow
	ByAI      int
	Totals    db.GroupTotalsRow
	Agencies  []db.GroupAgenciesRow
	Contracts []db.GroupContractsRow
	Crumbs    []crumb
}

func (h *Handler) supplierGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	g, err := h.q.GetSupplierGroup(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	page := groupPage{Period: p, Group: g, Crumbs: []crumb{{"Whole of government", "/"}, {"Suppliers", "/suppliers"}}}
	if page.Members, err = h.q.SupplierGroupMembers(ctx, id); err != nil {
		h.fail(w, err)
		return
	}
	for _, m := range page.Members {
		if m.Basis == "ai" {
			page.ByAI++
		}
	}
	if page.Totals, err = h.q.GroupTotals(ctx, id); err != nil {
		h.fail(w, err)
		return
	}
	if page.Agencies, err = h.q.GroupAgencies(ctx, id); err != nil {
		h.fail(w, err)
		return
	}
	if page.Contracts, err = h.q.GroupContracts(ctx, db.GroupContractsParams{GroupID: id, Lim: 100}); err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, "group.html", page)
}

// renderBlock executes one named template from a page, for htmx requests that
// replace part of it.
func (h *Handler) renderBlock(w http.ResponseWriter, page, block string, data any) {
	var buf bytes.Buffer
	if err := h.pages[page].ExecuteTemplate(&buf, block, data); err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
