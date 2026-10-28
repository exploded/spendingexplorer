package handlers

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/grantconnect"
	"spendingexplorer/internal/signals"
)

// noLimit is SQLite's LIMIT for every row, which the CSV downloads use.
const noLimit = -1

// sendCSV sends rows as a CSV download, as the bulk export writes them: a
// header row of snake_case names, whole dollars, and a byte-order mark so
// Excel reads the file as UTF-8. Each list's CSV leaves out what its page
// leaves out, such as suppliers who are individuals.
func sendCSV(w http.ResponseWriter, filename string, header []string, rows [][]string) {
	// The server's write timeout suits pages, not a long list on a slow link.
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	io.WriteString(w, string(rune(0xFEFF)))
	cw := csv.NewWriter(w)
	cw.Write(header)
	cw.WriteAll(rows)
	if err := cw.Error(); err != nil {
		slog.Warn("csv download cut short", "file", filename, "error", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func nullInt(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	return itoa(n.Int64)
}

func (h *Handler) roundRecipientsCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	goID := r.PathValue("go")
	if !goPattern.MatchString(goID) {
		http.NotFound(w, r)
		return
	}
	totals, err := h.q.RoundTotals(ctx, goID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if totals.Awards == 0 {
		http.NotFound(w, r)
		return
	}
	recs, err := h.q.RoundRecipients(ctx, db.RoundRecipientsParams{GoID: goID, Lim: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	site := siteURL(r)
	var rows [][]string
	for _, x := range recs {
		rows = append(rows, []string{x.Recipient, x.RecipientKey, itoa(x.Awards), itoa(x.Value), site + recipientURL(x.RecipientKey)})
	}
	sendCSV(w, "round-"+goID+"-recipients.csv",
		[]string{"recipient", "recipient_key", "awards", "value_whole_of_term", "url"}, rows)
}

func (h *Handler) recipientAwardsCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := grantconnect.KeyFromSlug(r.PathValue("key"))
	if key == "" {
		http.NotFound(w, r)
		return
	}
	if _, err := h.q.RecipientName(ctx, key); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	awards, err := h.q.RecipientAwards(ctx, db.RecipientAwardsParams{RecipientKey: key, Limit: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	var rows [][]string
	for _, a := range awards {
		rows = append(rows, []string{a.GaID, a.VersionID, a.Recipient, a.Activity, a.AgencyName, a.Category, a.GoID,
			a.PublishDate, a.StartDate, a.EndDate, itoa(a.Value)})
	}
	sendCSV(w, "recipient-"+grantconnect.RecipientSlug(key)+"-awards.csv",
		[]string{"ga_id", "version_id", "recipient", "activity", "agency", "category", "go_id",
			"publish_date", "start_date", "end_date", "value_whole_of_term"}, rows)
}

var contractHeader = []string{"cn_id", "description", "agency", "method", "published_date", "start_date", "end_date",
	"value_whole_of_life", "original_value", "amendment_count", "url"}

func (h *Handler) supplierContractsCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.q.GetSupplier(ctx, id); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	cs, err := h.q.SupplierContracts(ctx, db.SupplierContractsParams{SupplierID: sql.NullInt64{Int64: id, Valid: true}, Limit: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	site := siteURL(r)
	var rows [][]string
	for _, c := range cs {
		rows = append(rows, []string{c.CnID, c.Description, c.Agency, c.Method, c.PublishedDate, c.StartDate, c.EndDate,
			itoa(c.Value), nullInt(c.OriginalValue), itoa(c.AmendmentCount), site + "/contract/" + c.CnID})
	}
	sendCSV(w, "supplier-"+itoa(id)+"-contracts.csv", contractHeader, rows)
}

func (h *Handler) groupContractsCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.q.GetSupplierGroup(ctx, id); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	cs, err := h.q.GroupContracts(ctx, db.GroupContractsParams{GroupID: id, Lim: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	site := siteURL(r)
	var rows [][]string
	for _, c := range cs {
		rows = append(rows, []string{c.CnID, c.Description, c.Agency, c.Method, c.PublishedDate, c.StartDate, c.EndDate,
			itoa(c.Value), nullInt(c.OriginalValue), itoa(c.AmendmentCount), site + "/contract/" + c.CnID,
			itoa(c.SupplierID), c.Supplier})
	}
	sendCSV(w, "group-"+itoa(id)+"-contracts.csv", append(contractHeader, "supplier_id", "supplier"), rows)
}

func (h *Handler) suppliersCSV(w http.ResponseWriter, r *http.Request) {
	p, err := h.period(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	grouped := r.URL.Query().Get("view") != "abn"
	ranked, err := h.ranking(r.Context(), p.FY, grouped, noLimit, 0)
	if err != nil {
		h.fail(w, err)
		return
	}
	site := siteURL(r)
	var rows [][]string
	for i, x := range ranked {
		kind, link := "supplier", "/supplier/"
		if x.IsGroup {
			kind, link = "group", "/group/"
		}
		rows = append(rows, []string{strconv.Itoa(i + 1), kind, itoa(x.ID), x.Name, itoa(x.Suppliers), p.FY,
			itoa(x.Amount), itoa(x.LimitedAmount), itoa(x.Contracts), itoa(x.Entities), site + link + itoa(x.ID)})
	}
	name := "suppliers-by-group-" + p.FY + ".csv"
	if !grouped {
		name = "suppliers-by-abn-" + p.FY + ".csv"
	}
	sendCSV(w, name, []string{"rank", "kind", "id", "name", "suppliers", "fiscal_year",
		"amount_estimate", "limited_tender_amount_estimate", "contracts", "entities", "url"}, rows)
}

// signalList is a signal rule's list, as the rule's page filters it.
func (h *Handler) signalList(w http.ResponseWriter, r *http.Request) (signals.Rule, period, int64, bool) {
	rule, ok := signals.ByID(r.PathValue("rule"))
	if !ok {
		http.NotFound(w, r)
		return rule, period{}, 0, false
	}
	p, err := h.period(r.Context())
	if err != nil {
		h.fail(w, err)
		return rule, p, 0, false
	}
	entityID, _ := strconv.ParseInt(r.URL.Query().Get("entity"), 10, 64)
	return rule, p, max(entityID, 0), true
}

// signalFile names a signal list's CSV, such as
// signal-late-publication-entity-12-contracts.csv.
func signalFile(rule signals.Rule, entityID int64, list string) string {
	parts := []string{"signal", rule.ID}
	if entityID != 0 {
		parts = append(parts, "entity", itoa(entityID))
	}
	return strings.Join(append(parts, list), "-") + ".csv"
}

func (h *Handler) signalContractsCSV(w http.ResponseWriter, r *http.Request) {
	rule, p, entityID, ok := h.signalList(w, r)
	if !ok {
		return
	}
	cs, err := h.q.RuleContracts(r.Context(), db.RuleContractsParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: entityID, Lim: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	site := siteURL(r)
	var rows [][]string
	for _, c := range cs {
		rows = append(rows, []string{rule.ID, c.CnID, c.Description, c.Agency, nullInt(c.EntityID), c.FiscalYear,
			c.PublishedDate, c.StartDate, itoa(c.Amount), strconv.FormatFloat(c.Measure, 'f', -1, 64),
			measureLabel(rule.ID, c.Measure), itoa(c.Value), nullInt(c.OriginalValue), site + "/contract/" + c.CnID})
	}
	sendCSV(w, signalFile(rule, entityID, "contracts"), []string{"rule_id", "cn_id", "description", "agency", "entity_id",
		"fiscal_year", "published_date", "start_date", "amount_involved", "measure", "measure_text",
		"value_whole_of_life", "original_value", "url"}, rows)
}

func (h *Handler) signalGroupsCSV(w http.ResponseWriter, r *http.Request) {
	rule, p, entityID, ok := h.signalList(w, r)
	if !ok {
		return
	}
	if rule.Group != signals.GroupSupplier {
		http.NotFound(w, r)
		return
	}
	gs, err := h.q.RuleSupplierGroups(r.Context(), db.RuleSupplierGroupsParams{RuleID: rule.ID, Fy: p.FY, PrevFy: p.Prev, EntityID: entityID, Lim: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	site := siteURL(r)
	var rows [][]string
	for _, g := range gs {
		rows = append(rows, []string{rule.ID, g.Agency, nullInt(g.EntityID), itoa(g.SupplierID), g.Supplier, g.FiscalYear,
			itoa(g.Contracts), itoa(g.Amount), g.TopReason, itoa(g.Reasons), site + "/supplier/" + itoa(g.SupplierID)})
	}
	sendCSV(w, signalFile(rule, entityID, "groups"), []string{"rule_id", "agency", "entity_id", "supplier_id", "supplier",
		"fiscal_year", "contracts", "amount_involved", "most_common_reason", "reasons", "url"}, rows)
}

func (h *Handler) signalBatchesCSV(w http.ResponseWriter, r *http.Request) {
	rule, p, entityID, ok := h.signalList(w, r)
	if !ok {
		return
	}
	if rule.Group != signals.GroupBatch {
		http.NotFound(w, r)
		return
	}
	bs, err := h.q.RuleBatches(r.Context(), db.RuleBatchesParams{Fy: p.FY, PrevFy: p.Prev, EntityID: entityID, Lim: noLimit})
	if err != nil {
		h.fail(w, err)
		return
	}
	var rows [][]string
	for _, b := range bs {
		rows = append(rows, []string{rule.ID, b.Agency, nullInt(b.EntityID), b.PublishedDate, itoa(b.Notices), itoa(b.Amount), b.EarliestStart})
	}
	sendCSV(w, signalFile(rule, entityID, "batches"), []string{"rule_id", "agency", "entity_id", "published_date",
		"notices", "value", "earliest_start"}, rows)
}
