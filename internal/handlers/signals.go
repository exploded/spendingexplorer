package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
	"spendingexplorer/internal/signals"
)

// signalRow is a rule with how often it matched in a scope.
type signalRow struct {
	Rule      signals.Rule
	Contracts int64
	Amount    int64
	Agencies  int64
	Groups    int64                // grouped rules only
	Examples  []db.RuleExamplesRow // signals index only
}

// measureLabel states a signal's rule-specific measure in words.
func measureLabel(ruleID string, m float64) string {
	switch ruleID {
	case "value-increase":
		return fmt.Sprintf("+%.0f%% on first published value", m)
	case "late-publication":
		return fmt.Sprintf("published %.0f days after start", m)
	case "late-batch":
		return fmt.Sprintf("one of %.0f late notices the agency published that day", m)
	case "threshold-cluster":
		return fmt.Sprintf("one of %.0f contracts just under the threshold", m)
	case "repeat-limited":
		return fmt.Sprintf("one of %.0f limited tenders to this supplier", m)
	case "crossed-threshold":
		return "first published at " + explore.Money(int64(m))
	case "new-abn":
		return fmt.Sprintf("ABN active %.0f days before award", m)
	}
	return ""
}

func (h *Handler) entitySignals(r *http.Request, entityID int64, p period) ([]signalRow, error) {
	rows, err := h.q.EntitySignalSummary(r.Context(), db.EntitySignalSummaryParams{
		EntityID: sqlNullInt(entityID), Fy: p.FY, PrevFy: p.Prev,
	})
	if err != nil {
		return nil, err
	}
	by := map[string]db.EntitySignalSummaryRow{}
	for _, s := range rows {
		by[s.RuleID] = s
	}
	var out []signalRow
	for _, rule := range signals.Rules {
		if s, ok := by[rule.ID]; ok {
			out = append(out, signalRow{Rule: rule, Contracts: s.Contracts, Amount: s.Amount})
		}
	}
	return out, nil
}

type signalsPage struct {
	Period period
	Rows   []signalRow
}

func (h *Handler) signalsIndex(w http.ResponseWriter, r *http.Request) {
	p, err := h.period(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	totals, err := h.q.RuleTotals(r.Context(), db.RuleTotalsParams{Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	by := map[string]db.RuleTotalsRow{}
	for _, t := range totals {
		by[t.RuleID] = t
	}
	groups, err := h.q.RuleGroupCounts(r.Context(), db.RuleGroupCountsParams{Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	groupsBy := map[string]int64{}
	for _, g := range groups {
		groupsBy[g.RuleID] = g.GroupCount
	}
	examples, err := h.q.RuleExamples(r.Context(), db.RuleExamplesParams{Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	examplesBy := map[string][]db.RuleExamplesRow{}
	for _, e := range examples {
		examplesBy[e.RuleID] = append(examplesBy[e.RuleID], e)
	}
	page := signalsPage{Period: p}
	for _, rule := range signals.Rules {
		t := by[rule.ID]
		page.Rows = append(page.Rows, signalRow{Rule: rule, Contracts: t.Contracts, Amount: t.Amount, Agencies: t.Agencies, Groups: groupsBy[rule.ID], Examples: examplesBy[rule.ID]})
	}
	h.render(w, "signals.html", page)
}

type rulePage struct {
	Period    period
	Rule      signals.Rule
	Agencies  []db.RuleTopAgenciesRow
	Groups    []db.RuleSupplierGroupsRow // Rule.Group == GroupSupplier
	Batches   []db.RuleBatchesRow        // Rule.Group == GroupBatch
	Reasons   []db.RuleReasonsRow        // Rule.Reasons
	Contracts []db.RuleContractsRow      // per-contract rules, or grouped rules filtered to one entity
	Entity    string                     // name when filtered to one entity
	EntityID  int64

	GroupsPager    pager // Groups or Batches
	ContractsPager pager
}

// signalPageSize is how many rows a page of a signal's lists shows.
const signalPageSize = 100

func (h *Handler) signalRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rule, ok := signals.ByID(r.PathValue("rule"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := rulePage{Period: p, Rule: rule}
	if id, err := strconv.ParseInt(r.URL.Query().Get("entity"), 10, 64); err == nil && id > 0 {
		if ent, err := h.q.GetEntityInDoc(ctx, db.GetEntityInDocParams{EntityID: id, DocID: p.Doc.ID}); err == nil {
			page.Entity, page.EntityID = ent.Name, id
		}
	}
	if page.EntityID == 0 {
		if page.Agencies, err = h.q.RuleTopAgencies(ctx, db.RuleTopAgenciesParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev}); err != nil {
			h.fail(w, err)
			return
		}
	}
	base := "/signals/" + rule.ID + "/"
	contractsParam := "page"
	var total int64
	switch rule.Group {
	case signals.GroupSupplier:
		if total, err = h.q.RuleSupplierGroupCount(ctx, db.RuleSupplierGroupCountParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID}); err != nil {
			h.fail(w, err)
			return
		}
		if page.GroupsPager, ok = newPager(r, signalPageSize, total, csvURL(r, base+"groups.csv"), "groups"); !ok {
			http.NotFound(w, r)
			return
		}
		page.Groups, err = h.q.RuleSupplierGroups(ctx, db.RuleSupplierGroupsParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID,
			Lim: page.GroupsPager.Lim(), Off: page.GroupsPager.Off()})
		contractsParam = "cpage"
	case signals.GroupBatch:
		if total, err = h.q.RuleBatchCount(ctx, db.RuleBatchCountParams{Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID}); err != nil {
			h.fail(w, err)
			return
		}
		if page.GroupsPager, ok = newPager(r, signalPageSize, total, csvURL(r, base+"batches.csv"), "batches"); !ok {
			http.NotFound(w, r)
			return
		}
		page.Batches, err = h.q.RuleBatches(ctx, db.RuleBatchesParams{Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID,
			Lim: page.GroupsPager.Lim(), Off: page.GroupsPager.Off()})
		contractsParam = "cpage"
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	if rule.Reasons {
		if page.Reasons, err = h.q.RuleReasons(ctx, db.RuleReasonsParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID}); err != nil {
			h.fail(w, err)
			return
		}
	}
	// Grouped rules list contracts only for one entity; across all agencies the
	// groups are the unit, and one batch or supplier would fill the list.
	if rule.Group == "" || page.EntityID != 0 {
		if total, err = h.q.RuleContractCount(ctx, db.RuleContractCountParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID}); err != nil {
			h.fail(w, err)
			return
		}
		if page.ContractsPager, ok = newPagerAt(r, contractsParam, signalPageSize, total, csvURL(r, base+"contracts.csv"), "contracts"); !ok {
			http.NotFound(w, r)
			return
		}
		if page.Contracts, err = h.q.RuleContracts(ctx, db.RuleContractsParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: page.EntityID,
			Lim: page.ContractsPager.Lim(), Off: page.ContractsPager.Off()}); err != nil {
			h.fail(w, err)
			return
		}
	}
	h.render(w, "signal.html", page)
}
