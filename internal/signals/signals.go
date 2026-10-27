// Package signals defines the "worth a closer look" rules. Each rule is a
// measurable pattern in published records, stated with its threshold and the
// common innocent explanations. No rule asserts a motive or wrongdoing.
package signals

// Rule describes one signal for the site and methodology.
type Rule struct {
	ID        string
	Title     string
	Pattern   string   // what the rule measures, in one sentence
	Threshold string   // when it fires
	Context   string   // the rule or practice the pattern relates to
	Innocent  []string // common legitimate explanations
	Group     string   // how matches are listed: "" per contract, GroupSupplier or GroupBatch
	Reasons   bool     // show the limited-tender reasons agencies gave
}

// Ways a rule's matches are grouped for counting and listing.
const (
	GroupSupplier = "supplier" // one agency, one supplier, one financial year
	GroupBatch    = "batch"    // one agency, one publication day
)

// Open-tender thresholds (CPR paragraph 9.7). Approaches to market from 17
// November 2025 use the higher non-corporate figure; the rules use the
// publication date as a stand-in for the approach date.
const thresholdText = "$80,000, or $125,000 for contracts published from 17 November 2025; $400,000 for corporate entities; $7.5 million for construction"

var Rules = []Rule{
	{
		ID:        "value-increase",
		Title:     "Value increased after award",
		Pattern:   "The contract’s value rose after it was first published.",
		Threshold: "An increase of at least 50% and at least $1 million from the first published value.",
		Context:   "Agencies must publish amendments that change a contract’s value. Large increases can mean the original scope or price was understated.",
		Innocent: []string{
			"Additional work or an extended term agreed after award.",
			"Options in the original contract that were later taken up.",
			"A correction to a value entered wrongly.",
		},
	},
	{
		ID:        "late-publication",
		Title:     "Published long after it started",
		Pattern:   "The contract notice was published more than six months after the contract’s start date.",
		Threshold: "Publication more than 180 days after the start date, for contracts worth $100,000 or more. AusTender does not publish the date a contract was signed, so we measure from the start date and allow a wide margin. Notices published in a batch are counted under “Published late in a batch” instead.",
		Context:   "The Commonwealth Procurement Rules require agencies to report contracts on AusTender within 42 days of entering into them.",
		Innocent: []string{
			"A start date set earlier than signing, to cover work under an earlier arrangement.",
			"A lease or agreement republished under a new notice when it was renewed or varied.",
			"Administrative delay in reporting.",
		},
	},
	{
		ID:        "late-batch",
		Title:     "Published late in a batch",
		Pattern:   "An agency published many contract notices on one day, each more than six months after the contract started.",
		Threshold: "Ten or more notices from one agency on the same day, each published more than 180 days after its start date and worth $100,000 or more. Each batch counts once.",
		Context:   "The Commonwealth Procurement Rules require agencies to report contracts on AusTender within 42 days of entering into them. A batch usually means a reporting backlog was cleared at once.",
		Innocent: []string{
			"A new agency, or one changed by a machinery-of-government move, publishing contracts it took over.",
			"A review that found unreported contracts, followed by a correction.",
			"Leases or standing arrangements reported again under new notices.",
		},
		Group: GroupBatch,
	},
	{
		ID:        "threshold-cluster",
		Title:     "Several contracts just under the open-tender threshold",
		Pattern:   "The same agency awarded the same supplier several contracts by limited tender, each valued just below the open-tender threshold, in one financial year.",
		Threshold: "Three or more limited-tender contracts within 10% below the threshold (" + thresholdText + ") from one agency to one supplier, published in the same financial year. Open-tender and panel contracts, and contracts under Appendix A exemptions, are not counted.",
		Context:   "For non-corporate Commonwealth entities, open tender is generally required at or above the threshold. Splitting a requirement to stay under it is not permitted.",
		Innocent: []string{
			"Separate, genuine requirements that happen to be similar in size.",
			"Call-offs from a standing offer or panel with fixed engagement sizes.",
			"A construction contract outside AusTender category 72, which we cannot tell apart from other services.",
		},
		Group:   GroupSupplier,
		Reasons: true,
	},
	{
		ID:        "repeat-limited",
		Title:     "Repeated limited tenders to one supplier",
		Pattern:   "The same agency awarded the same supplier several contracts by limited tender in one financial year.",
		Threshold: "Five or more limited-tender contracts at or above the open-tender threshold (" + thresholdText + "), worth $1 million or more in total, published in the same financial year. Contracts below the threshold, where limited tender is routine, and contracts under Appendix A exemptions (such as legal counsel) are not counted.",
		Context:   "Above the threshold, limited tender is allowed only in defined circumstances, such as urgency or a single capable supplier. Repeated use with one supplier reduces competition.",
		Innocent: []string{
			"A specialised supplier with no practical alternative.",
			"Additional deliveries that must match existing equipment or systems.",
			"Urgent requirements, such as disaster response.",
		},
		Group:   GroupSupplier,
		Reasons: true,
	},
	{
		ID:        "crossed-threshold",
		Title:     "Grew past the open-tender threshold",
		Pattern:   "A contract awarded by limited tender below the open-tender threshold later rose in value to the threshold or above.",
		Threshold: "Limited tender, first published below the threshold (" + thresholdText + "), with a current value at least 25% above the threshold and at least 50% above the first published value. Contracts under Appendix A exemptions are not counted.",
		Context:   "The Commonwealth Procurement Rules require an agency to estimate a procurement’s maximum value, including options and extensions, before choosing how to buy. Open tender is generally required at or above the threshold.",
		Innocent: []string{
			"Extra work that could not have been foreseen at award.",
			"A nominal value published at award and updated once the actual amount was known.",
			"A construction contract outside AusTender category 72, which we cannot tell apart from other services.",
		},
		Reasons: true,
	},
}

// ByID returns the rule with the given ID.
func ByID(id string) (Rule, bool) {
	for _, r := range Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}
