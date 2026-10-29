package handlers

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/explore"
	"spendingexplorer/internal/pbs"
)

// fundingNote explains, on a corporate entity's page, the funding it receives
// from other Commonwealth entities: how much there is, which program pays it,
// and what the amount counted at the entity is made of.
type fundingNote struct {
	Gross        int64 // counted here plus funding counted where it is paid from
	Funded       int64
	Appropriated int64 // the part of Funded appropriated by Parliament and paid through the portfolio department
	Counted      int64
	Payers       []payerAmount
	Unmatched    int64 // funding no single paying line could be found for
	Portfolio    string
	PortfolioURL string
	Largest      string // the largest counted line item, when it is most of Counted
	LargestAmt   int64
}

type payerAmount struct {
	Name   string
	URL    string
	Amount int64
}

func (h *Handler) entityFunding(ctx context.Context, p period, id int64, name string, counted int64) (*fundingNote, error) {
	lines, err := h.q.EntityFundingLines(ctx, db.EntityFundingLinesParams{DocID: p.Doc.ID, EntityID: id, Fy: p.FY})
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	f := &fundingNote{Counted: counted, Portfolio: lines[0].Portfolio, PortfolioURL: "/portfolio/" + url.PathEscape(lines[0].Portfolio)}
	cands, err := h.q.PortfolioPayerLines(ctx, db.PortfolioPayerLinesParams{DocID: p.Doc.ID, Portfolio: f.Portfolio, Fy: p.FY, EntityID: id})
	if err != nil {
		return nil, err
	}
	amounts := make([]int64, len(lines))
	for i, l := range lines {
		amounts[i] = l.Amount
		f.Funded += l.Amount
		if appropriated(l.LineItem) {
			f.Appropriated += l.Amount
		}
	}
	payerLines := make([]pbs.PayerLine, len(cands))
	for i, c := range cands {
		payerLines[i] = pbs.PayerLine{LineItem: c.LineItem, Amount: c.Amount}
	}
	byProgram := map[int64]int{}
	for i, j := range pbs.MatchPayers(name, amounts, payerLines) {
		if j < 0 {
			f.Unmatched += amounts[i]
			continue
		}
		c := cands[j]
		k, ok := byProgram[c.ProgramID]
		if !ok {
			k = len(f.Payers)
			byProgram[c.ProgramID] = k
			f.Payers = append(f.Payers, payerAmount{Name: c.EntityName + ", " + explore.DisplayName(c.ProgramName), URL: "/program/" + strconv.FormatInt(c.ProgramID, 10)})
		}
		f.Payers[k].Amount += amounts[i]
	}
	f.Gross = f.Funded + f.Counted
	largest, err := h.q.EntityCountedLines(ctx, db.EntityCountedLinesParams{DocID: p.Doc.ID, EntityID: id, Fy: p.FY})
	if err != nil {
		return nil, err
	}
	// Negative lines can leave the largest line above the total; "mostly" would then mislead.
	if len(largest) == 1 && largest[0].Amount <= counted && 2*largest[0].Amount > counted {
		f.Largest, f.LargestAmt = explore.DisplayName(largest[0].LineItem), largest[0].Amount
	}
	return f, nil
}

// appropriated reports whether a funding line is the entity's own
// appropriation, which Parliament makes to the portfolio department to pass
// on, as against payments other entities make to it for work or services.
func appropriated(lineItem string) bool {
	l := strings.ToLower(lineItem)
	return strings.Contains(l, "appropriation") || strings.Contains(l, "ordinary annual services")
}
