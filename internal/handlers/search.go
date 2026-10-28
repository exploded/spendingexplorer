package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"spendingexplorer/internal/db"
)

// searchLimit is how many results each kind of record lists.
const searchLimit = 20

// cnPattern is an AusTender contract notice ID, such as CN4012345 or CN4012345-A1.
var cnPattern = regexp.MustCompile(`^CN\d+(-A\d+)?$`)

type searchPage struct {
	Period     period
	Crumbs     []crumb
	Query      string
	Searched   bool
	TooShort   bool
	Entities   []db.SearchEntitiesRow
	Portfolios []string
	Programs   []db.SearchProgramsRow
	Projects   []projectView
	Groups     []db.SearchSupplierGroupsRow
	Suppliers  []db.SearchSuppliersRow
	Contracts  []db.SearchContractsRow
	Grants     []db.SearchGrantRecipientsRow
}

// Matches counts every result listed, for the summary line.
func (p searchPage) Matches() int {
	return len(p.Entities) + len(p.Portfolios) + len(p.Programs) + len(p.Projects) +
		len(p.Groups) + len(p.Suppliers) + len(p.Contracts) + len(p.Grants)
}

// ContractMatches is how many contracts matched, including those not listed.
func (p searchPage) ContractMatches() int64 {
	if len(p.Contracts) == 0 {
		return 0
	}
	return p.Contracts[0].Matches
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.period(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	page := searchPage{Period: p, Crumbs: []crumb{{"Whole of government", "/"}}, Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	switch n := len([]rune(page.Query)); {
	case n == 1:
		page.TooShort = true
	case n >= 2:
		page.Searched = true
		if err := h.searchAll(r, &page); err != nil {
			h.fail(w, err)
			return
		}
	}
	// The search box asks for its results only.
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "site-results" {
		h.renderBlock(w, "search.html", "site-results", page)
		return
	}
	h.render(w, "search.html", page)
}

// searchAll runs one query per kind of record. They run at once because the
// contract and grant queries scan every row (about 200 ms each).
func (h *Handler) searchAll(r *http.Request, page *searchPage) error {
	ctx, q, p := r.Context(), page.Query, page.Period
	cn := strings.ToUpper(strings.Join(strings.Fields(q), ""))
	if !cnPattern.MatchString(cn) {
		cn = ""
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	wg.Go(func() {
		page.Entities, errs[0] = h.q.SearchEntities(ctx, db.SearchEntitiesParams{Fy: p.FY, Fragment: q, DocID: p.Doc.ID, Lim: searchLimit})
	})
	wg.Go(func() {
		page.Portfolios, errs[1] = h.q.SearchPortfolios(ctx, db.SearchPortfoliosParams{BudgetYear: p.FY, Fragment: q})
	})
	wg.Go(func() {
		page.Programs, errs[2] = h.q.SearchPrograms(ctx, db.SearchProgramsParams{Fy: p.FY, DocID: p.Doc.ID, Fragment: q, Lim: searchLimit})
	})
	wg.Go(func() {
		var projects []projectView
		projects, errs[3] = h.projects(ctx)
		upper := strings.ToUpper(q)
		for _, pv := range projects {
			if strings.Contains(strings.ToUpper(pv.Name+"\x00"+pv.Owner+"\x00"+pv.Summary), upper) {
				page.Projects = append(page.Projects, pv)
			}
		}
	})
	wg.Go(func() {
		page.Groups, errs[4] = h.q.SearchSupplierGroups(ctx, db.SearchSupplierGroupsParams{Fragment: q, Lim: searchLimit})
	})
	wg.Go(func() {
		page.Suppliers, errs[5] = h.searchSuppliers(r, q, searchLimit)
	})
	wg.Go(func() {
		page.Contracts, errs[6] = h.q.SearchContracts(ctx, db.SearchContractsParams{CnID: cn, Fragment: q, Lim: searchLimit})
	})
	wg.Go(func() {
		page.Grants, errs[7] = h.q.SearchGrantRecipients(ctx, db.SearchGrantRecipientsParams{Fragment: q, Lim: searchLimit})
	})
	wg.Wait()
	return errors.Join(errs...)
}
