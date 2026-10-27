package pbs

import "strings"

// Treatment decides whether a line counts towards itemised spending.
// The PBS tables list some money twice: once where it is paid out and once
// where it is received or spent again. Each rule has a stable ID so the site
// can show which rule removed a line and why.
type Treatment struct {
	Kind   string // expense | transfer | subtotal | other_economic_flow
	RuleID string
}

// Rule documents a treatment rule for the methodology page.
type Rule struct {
	ID     string
	Kind   string
	Short  string // label used in badges and the reconciliation
	Reason string
}

var Rules = []Rule{
	{"line-item", "expense", "reported", "Published program expense line."},
	{"subtotal", "subtotal", "subtotal rows", "A total row inside the table (for example \"Totals for Outcome 1\"); its parts are already counted."},
	{"corporate-commonwealth-funded", "transfer", "funding already counted at the paying department",
		"Corporate entity funding paid by its portfolio department. The department already records this as an administered expense, so counting it again at the corporate entity would double count it."},
	{"medicare-guarantee-fund-credit", "transfer", "Medicare Guarantee Fund credits",
		"Treasury credits the Medicare Guarantee Fund Special Account. Health records the medical and pharmaceutical benefits paid out of it."},
	{"tax-bad-debts", "other_economic_flow", "tax bad and doubtful debts",
		"ATO bad and doubtful debts and remissions. The Budget's government finance statistics treat these as write-downs of assets (an \"other economic flow\"), not expenses, so they sit outside the Budget Paper 1 total."},
}

// corporateFundingFromCommonwealth matches corporate-entity funding lines that
// are paid by another Commonwealth entity.
var corporateFundingFromCommonwealth = []string{
	"related entit",
	"portfolio department",
	"payment from treasury",
	"other government entities",
	"grants received from portfolio",
	"ordinary annual services",
	"other services (appropriation",
	"appropriation bill",
	"appropriation (parliamentary",
	"payment from portfolio",
	"amounts from the portfolio",
}

// Classify applies the treatment rules to a line.
func Classify(l Line) Treatment {
	item := strings.ToLower(l.LineItem)
	switch {
	case strings.HasPrefix(item, "total"), strings.HasPrefix(strings.ToLower(l.Program), "total"):
		return Treatment{"subtotal", "subtotal"}
	case l.Kind == "corporate" && !strings.Contains(item, "not requiring appropriation") && containsAny(item, corporateFundingFromCommonwealth):
		return Treatment{"transfer", "corporate-commonwealth-funded"}
	case l.Entity == "Department of the Treasury" && strings.Contains(item, "medicare guarantee fund special account"):
		return Treatment{"transfer", "medicare-guarantee-fund-credit"}
	case l.Entity == "Australian Taxation Office" && strings.Contains(strings.ToLower(l.Program), "doubtful debts"):
		return Treatment{"other_economic_flow", "tax-bad-debts"}
	}
	return Treatment{"expense", "line-item"}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// EstimateType labels a fiscal-year column relative to the publication's budget year:
// the year before is an estimated actual, the budget year is the budget estimate and
// later years are forward estimates.
func EstimateType(budgetYear, fiscalYear string) string {
	switch {
	case fiscalYear < budgetYear:
		return "estimated_actual"
	case fiscalYear == budgetYear:
		return "budget"
	}
	return "forward_estimate"
}
