package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/classify"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
	"spendingexplorer/internal/signals"
)

// coverage is how much of a node's total can be traced to contract notices.
type coverage struct {
	Available bool   // contracts have been loaded
	Since     string // earliest publication date loaded
	Amount    int64  // annualised contract value in the year (estimate)
	Share     float64
	Note      string // shown instead of the meter when coverage does not apply
}

func (h *Handler) coverageFor(ctx context.Context, amount, total int64) coverage {
	s, err := h.q.ContractLoadSummary(ctx)
	if err != nil || s.Contracts == 0 {
		return coverage{}
	}
	return coverage{Available: true, Since: s.FirstPublished, Amount: amount, Share: explore.Share(amount, total)}
}

// contractsSection is the "Who gets paid" part of an entity page.
type contractsSection struct {
	Stats     db.EntityContractStatsRow
	Suppliers []db.EntityTopSuppliersRow
	Contracts []db.EntityTopContractsRow
}

func (h *Handler) entityContracts(ctx context.Context, entityID int64, fy string) (*contractsSection, error) {
	stats, err := h.q.EntityContractStats(ctx, db.EntityContractStatsParams{Fy: fy, EntityID: sql.NullInt64{Int64: entityID, Valid: true}})
	if err != nil || stats.Contracts == 0 {
		return nil, err
	}
	sup, err := h.q.EntityTopSuppliers(ctx, db.EntityTopSuppliersParams{Fy: fy, EntityID: sql.NullInt64{Int64: entityID, Valid: true}, Lim: 15})
	if err != nil {
		return nil, err
	}
	con, err := h.q.EntityTopContracts(ctx, db.EntityTopContractsParams{Fy: fy, EntityID: sql.NullInt64{Int64: entityID, Valid: true}, Lim: 15})
	if err != nil {
		return nil, err
	}
	return &contractsSection{Stats: stats, Suppliers: sup, Contracts: con}, nil
}

type supplierPage struct {
	Period    period
	Supplier  db.Supplier
	Totals    db.SupplierTotalsRow
	Agencies  []db.SupplierAgenciesRow
	Contracts []db.SupplierContractsRow
	Group     *db.SupplierGroupOfRow
	Crumbs    []crumb
}

func (h *Handler) supplier(w http.ResponseWriter, r *http.Request) {
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
	s, err := h.q.GetSupplier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	page := supplierPage{Period: p, Supplier: s, Crumbs: []crumb{{"Whole of government", "/"}, {"Suppliers", "/suppliers"}}}
	if g, err := h.q.SupplierGroupOf(ctx, id); err == nil {
		page.Group = &g
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.fail(w, err)
		return
	}
	if page.Totals, err = h.q.SupplierTotals(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
		h.fail(w, err)
		return
	}
	if page.Agencies, err = h.q.SupplierAgencies(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
		h.fail(w, err)
		return
	}
	if page.Contracts, err = h.q.SupplierContracts(ctx, db.SupplierContractsParams{SupplierID: sql.NullInt64{Int64: id, Valid: true}, Limit: 100}); err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, "supplier.html", page)
}

type contractPage struct {
	Period   period
	Contract db.GetContractRow
	Releases []db.ContractReleasesRow
	Years    []db.ContractFiscalYearsRow
	Crumbs   []crumb
	APIURL   string
	Change   explore.Node // original -> current value
	Signals  []contractSignal
	AIClass  *db.ContractClassificationRow
	AILabel  string
}

type contractSignal struct {
	Rule    signals.Rule
	Measure float64
}

func (h *Handler) contract(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cn := r.PathValue("cn")
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	c, err := h.q.GetContract(ctx, cn)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	page := contractPage{Period: p, Contract: c, APIURL: austender.ByIDURL(cn), Crumbs: []crumb{{"Whole of government", "/"}}}
	if c.EntityID.Valid {
		if ent, err := h.q.GetEntityInDoc(ctx, db.GetEntityInDocParams{EntityID: c.EntityID.Int64, DocID: p.Doc.ID}); err == nil {
			page.Crumbs = append(page.Crumbs,
				crumb{ent.Portfolio, "/portfolio/" + url.PathEscape(ent.Portfolio)},
				crumb{ent.Name, "/entity/" + strconv.FormatInt(ent.ID, 10)})
		}
	}
	if c.OriginalValue.Valid {
		page.Change = explore.Node{Cur: c.Value, Prev: c.OriginalValue.Int64}
	}
	if page.Releases, err = h.q.ContractReleases(ctx, cn); err != nil {
		h.fail(w, err)
		return
	}
	if page.Years, err = h.q.ContractFiscalYears(ctx, cn); err != nil {
		h.fail(w, err)
		return
	}
	if ac, err := h.q.ContractClassification(ctx, db.ContractClassificationParams{CnID: cn, Task: classify.Task}); err == nil {
		page.AIClass, page.AILabel = &ac, classify.LabelOf(ac.Category)
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.fail(w, err)
		return
	}
	sigs, err := h.q.ContractSignals(ctx, cn)
	if err != nil {
		h.fail(w, err)
		return
	}
	for _, s := range sigs {
		if rule, ok := signals.ByID(s.RuleID); ok {
			page.Signals = append(page.Signals, contractSignal{Rule: rule, Measure: s.Measure})
		}
	}
	h.render(w, "contract.html", page)
}
