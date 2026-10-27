// Package handlers serves the explorer's pages. The database is read-only;
// loaders under cmd/ write it.
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
	"spendingexplorer/internal/pbs"
)

// treemapAspect is the treemap's width / height; the CSS uses the same ratio.
const treemapAspect = 2.2

type Handler struct {
	q     *db.Queries
	pages map[string]*template.Template
}

func New(q *db.Queries, templates fs.FS) (*Handler, error) {
	funcs := template.FuncMap{
		"money":       explore.Money,
		"signedMoney": explore.SignedMoney,
		"changePct": func(n explore.Node) string {
			if p, ok := n.ChangePct(); ok {
				return explore.Pct(p)
			}
			if n.Cur != 0 {
				return "new"
			}
			return "–"
		},
		"pct":    explore.Pct,
		"f1":     func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
		"pctCSS": func(f float64) template.CSS { return template.CSS(strconv.FormatFloat(f, 'f', 3, 64) + "%") },
		"share":  explore.Share,
		"estimate": func(t string) string {
			return map[string]string{
				"estimated_actual": "estimated actual",
				"budget":           "Budget estimate",
				"forward_estimate": "forward estimate",
				"actual":           "actual",
			}[t]
		},
		"estimateType": pbs.EstimateType,
		"tone":         tone,
		"lower":        strings.ToLower,
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i].(string)] = kv[i+1]
			}
			return m
		},
		"pathEscape": url.PathEscape,
	}
	h := &Handler{q: q, pages: map[string]*template.Template{}}
	for _, page := range []string{"node.html", "program.html", "methodology.html", "sources.html"} {
		t, err := template.New("").Funcs(funcs).ParseFS(templates, "layout.html", "partials.html", page)
		if err != nil {
			return nil, err
		}
		h.pages[page] = t
	}
	return h, nil
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /portfolio/{name}", h.portfolio)
	mux.HandleFunc("GET /entity/{id}", h.entity)
	mux.HandleFunc("GET /program/{id}", h.program)
	mux.HandleFunc("GET /methodology", h.methodology)
	mux.HandleFunc("GET /sources", h.sources)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
}

// period is the publication and years every page reports on.
type period struct {
	Doc  db.LatestPBSDocumentRow
	FY   string // the publication's budget year
	Prev string // the year before, an estimated actual in the same publication
}

func (h *Handler) period(ctx context.Context) (period, error) {
	doc, err := h.q.LatestPBSDocument(ctx)
	if err != nil {
		return period{}, err
	}
	return period{Doc: doc, FY: doc.BudgetYear, Prev: explore.PreviousYear(doc.BudgetYear)}, nil
}

type crumb struct{ Name, URL string }

// row is a child node in a table, with its share of the parent.
type row struct {
	explore.Node
	Share float64
}

type nodePage struct {
	Period     period
	Title      string
	Level      string
	ChildLabel string
	Crumbs     []crumb
	Total      explore.Node
	Tiles      []explore.Tile
	Rows       []row
	Sort       string
	Recon      *recon         // home only
	Functions  []row          // home only
	Kinds      []row          // entity only
	Omitted    []explore.Node // children left out of the treemap (zero or negative)
}

// recon reconciles program line items to the Budget Paper 1 total.
type recon struct {
	Gross       int64
	Removed     []removal
	RemovedByID map[string]*removal
	LineItems   int64
	WogTotal    int64
	Adjustment  int64
	WogTitle    string
	WogURL      string
}

type removal struct {
	Rule   pbs.Rule
	Amount int64
	Lines  int64
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	ports, err := h.q.PortfolioTotals(ctx, db.PortfolioTotalsParams{DocID: p.Doc.ID, Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	var nodes []explore.Node
	for _, pt := range ports {
		nodes = append(nodes, explore.Node{
			Name: pt.Portfolio, URL: "/portfolio/" + url.PathEscape(pt.Portfolio),
			Cur: pt.Cur, Prev: pt.Prev, Excluded: pt.Excluded, Children: int(pt.Children),
		})
	}

	rc, total, funcs, err := h.reconcile(ctx, p)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := h.nodePage(r, p, nodes, total)
	page.Title = "Whole of government"
	page.Level = "Australian Government general government sector"
	page.ChildLabel = "Portfolio"
	page.Recon = rc
	page.Functions = funcs
	h.render(w, "node.html", page)
}

// reconcile builds the line items → BP1 total reconciliation and the function lens.
func (h *Handler) reconcile(ctx context.Context, p period) (*recon, explore.Node, []row, error) {
	rc := &recon{RemovedByID: map[string]*removal{}}
	treat, err := h.q.TreatmentTotals(ctx, db.TreatmentTotalsParams{DocID: p.Doc.ID, Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		return nil, explore.Node{}, nil, err
	}
	var linePrev int64
	for _, t := range treat {
		switch t.Treatment {
		case "expense":
			rc.LineItems += t.Cur
			linePrev += t.Prev
			rc.Gross += t.Cur
		case "transfer", "other_economic_flow":
			rc.Gross += t.Cur
			rc.Removed = append(rc.Removed, removal{Rule: ruleByID(t.TreatmentRule), Amount: t.Cur, Lines: t.Lines})
		}
	}
	for i := range rc.Removed {
		rc.RemovedByID[rc.Removed[i].Rule.ID] = &rc.Removed[i]
	}

	total := explore.Node{Name: "Whole of government", Cur: rc.LineItems, Prev: linePrev}
	var funcs []row
	wdoc, err := h.q.WogDocument(ctx, p.FY)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return rc, total, nil, nil
	case err != nil:
		return nil, total, nil, err
	}
	rc.WogTitle, rc.WogURL = wdoc.Title, wdoc.Url
	ft, err := h.q.FunctionTotals(ctx, db.FunctionTotalsParams{DocID: wdoc.ID, Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		return nil, total, nil, err
	}
	for _, f := range ft {
		if f.Category == "Total expenses" {
			total.Cur, total.Prev = f.Cur, f.Prev
			continue
		}
		funcs = append(funcs, row{Node: explore.Node{Name: f.Category, Cur: f.Cur, Prev: f.Prev}})
	}
	for i := range funcs {
		funcs[i].Share = explore.Share(funcs[i].Cur, total.Cur)
	}
	rc.WogTotal = total.Cur
	rc.Adjustment = total.Cur - rc.LineItems
	return rc, total, funcs, nil
}

func (h *Handler) portfolio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	ents, err := h.q.EntityTotals(ctx, db.EntityTotalsParams{DocID: p.Doc.ID, Portfolio: name, Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	if len(ents) == 0 {
		http.NotFound(w, r)
		return
	}
	var nodes []explore.Node
	total := explore.Node{Name: name}
	for _, e := range ents {
		n := explore.Node{Name: e.Name, URL: "/entity/" + strconv.FormatInt(e.ID, 10), Cur: e.Cur, Prev: e.Prev, Excluded: e.Excluded, Children: int(e.Children)}
		nodes = append(nodes, n)
		total.Cur += n.Cur
		total.Prev += n.Prev
		total.Excluded += n.Excluded
	}
	page := h.nodePage(r, p, nodes, total)
	page.Title = name
	page.Level = "Portfolio"
	page.ChildLabel = "Entity"
	page.Crumbs = []crumb{{"Whole of government", "/"}}
	h.render(w, "node.html", page)
}

func (h *Handler) entity(w http.ResponseWriter, r *http.Request) {
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
	ent, err := h.q.GetEntityInDoc(ctx, db.GetEntityInDocParams{EntityID: id, DocID: p.Doc.ID})
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	progs, err := h.q.ProgramTotals(ctx, db.ProgramTotalsParams{DocID: p.Doc.ID, EntityID: id, Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	var nodes []explore.Node
	total := explore.Node{Name: ent.Name}
	for _, pr := range progs {
		n := explore.Node{Name: pr.Name, URL: "/program/" + strconv.FormatInt(pr.ID, 10), Cur: pr.Cur, Prev: pr.Prev, Excluded: pr.Excluded, Children: int(pr.Children)}
		nodes = append(nodes, n)
		total.Cur += n.Cur
		total.Prev += n.Prev
		total.Excluded += n.Excluded
	}
	kinds, err := h.q.KindTotals(ctx, db.KindTotalsParams{DocID: p.Doc.ID, EntityID: id, Fy: p.FY})
	if err != nil {
		h.fail(w, err)
		return
	}
	page := h.nodePage(r, p, nodes, total)
	page.Title = ent.Name
	page.Level = "Entity"
	page.ChildLabel = "Program"
	page.Crumbs = []crumb{{"Whole of government", "/"}, {ent.Portfolio, "/portfolio/" + url.PathEscape(ent.Portfolio)}}
	for _, k := range kinds {
		page.Kinds = append(page.Kinds, row{Node: explore.Node{Name: kindLabel[k.ExpenseKind], Cur: k.Cur}, Share: explore.Share(k.Cur, total.Cur)})
	}
	h.render(w, "node.html", page)
}

var kindLabel = map[string]string{
	"departmental": "Departmental (running costs: staff, suppliers, property)",
	"administered": "Administered (payments on the government's behalf)",
	"corporate":    "Corporate entity, funded other than by its department",
}

// nodePage fills the parts every hierarchy page shares: treemap, sorted table.
func (h *Handler) nodePage(r *http.Request, p period, nodes []explore.Node, total explore.Node) nodePage {
	page := nodePage{Period: p, Total: total, Sort: r.URL.Query().Get("sort")}
	page.Tiles = explore.Treemap(nodes, treemapAspect)
	var parent int64
	for _, n := range nodes {
		parent += max(n.Cur, 0)
		if n.Cur <= 0 {
			page.Omitted = append(page.Omitted, n)
		}
	}
	for _, n := range nodes {
		page.Rows = append(page.Rows, row{Node: n, Share: explore.Share(n.Cur, parent)})
	}
	// Default order surfaces the largest increases first.
	less := map[string]func(a, b row) bool{
		"size": func(a, b row) bool { return a.Cur > b.Cur },
		"pct": func(a, b row) bool {
			pa, oka := a.ChangePct()
			pb, okb := b.ChangePct()
			if oka != okb {
				return oka
			}
			return pa > pb
		},
		"name":   func(a, b row) bool { return a.Name < b.Name },
		"change": func(a, b row) bool { return a.Change() > b.Change() },
	}
	if _, ok := less[page.Sort]; !ok {
		page.Sort = "change"
	}
	sort.SliceStable(page.Rows, func(i, j int) bool { return less[page.Sort](page.Rows[i], page.Rows[j]) })
	return page
}

type programPage struct {
	Period  period
	Program db.GetProgramRow
	Crumbs  []crumb
	Total   explore.Node
	Lines   []programLine
}

type programLine struct {
	db.ProgramLinesRow
	Node explore.Node
	Rule pbs.Rule
}

func (h *Handler) program(w http.ResponseWriter, r *http.Request) {
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
	prog, err := h.q.GetProgram(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && prog.BudgetYear != p.FY) {
		// Programs from other budget years will redirect once lineage is loaded.
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, err)
		return
	}
	lines, err := h.q.ProgramLines(ctx, db.ProgramLinesParams{DocID: p.Doc.ID, ProgramID: id, Fy: p.FY, PrevFy: p.Prev})
	if err != nil {
		h.fail(w, err)
		return
	}
	page := programPage{
		Period:  p,
		Program: prog,
		Crumbs: []crumb{
			{"Whole of government", "/"},
			{prog.Portfolio, "/portfolio/" + url.PathEscape(prog.Portfolio)},
			{prog.EntityName, "/entity/" + strconv.FormatInt(prog.EntityID, 10)},
		},
		Total: explore.Node{Name: prog.Name},
	}
	for _, l := range lines {
		pl := programLine{ProgramLinesRow: l, Node: explore.Node{Name: l.LineItem, Cur: l.Cur, Prev: l.Prev}, Rule: ruleByID(l.TreatmentRule)}
		if l.Treatment == "expense" {
			page.Total.Cur += l.Cur
			page.Total.Prev += l.Prev
		} else if l.Treatment != "subtotal" {
			page.Total.Excluded += l.Cur
		}
		page.Lines = append(page.Lines, pl)
	}
	h.render(w, "program.html", page)
}

type methodologyPage struct {
	Period period
	Rules  []pbs.Rule
	Recon  *recon
	Total  explore.Node
}

func (h *Handler) methodology(w http.ResponseWriter, r *http.Request) {
	p, err := h.period(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	rc, total, _, err := h.reconcile(r.Context(), p)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, "methodology.html", methodologyPage{Period: p, Rules: pbs.Rules, Recon: rc, Total: total})
}

type sourcesPage struct {
	Period  period
	Sources []db.SourceDocument
	Runs    []db.ListLoadRunsRow
}

func (h *Handler) sources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	src, err := h.q.ListSourceDocuments(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	runs, err := h.q.ListLoadRuns(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, "sources.html", sourcesPage{Period: p, Sources: src, Runs: runs})
}

// tone buckets a node's year-on-year change for the treemap's diverging scale.
func tone(n explore.Node) string {
	p, ok := n.ChangePct()
	switch {
	case !ok && n.Cur > 0:
		return "up2" // new this year
	case !ok:
		return "flat"
	case p <= -10:
		return "down2"
	case p <= -2:
		return "down1"
	case p < 2:
		return "flat"
	case p < 10:
		return "up1"
	}
	return "up2"
}

func ruleByID(id string) pbs.Rule {
	for _, r := range pbs.Rules {
		if r.ID == id {
			return r
		}
	}
	return pbs.Rule{ID: id}
}

func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	var buf bytes.Buffer
	if err := h.pages[page].ExecuteTemplate(&buf, "layout", data); err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
