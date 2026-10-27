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
}

var Rules = []Rule{
	{
		ID:        "value-increase",
		Title:     "Value increased after award",
		Pattern:   "The contract’s value rose after it was first published.",
		Threshold: "An increase of at least 50% and at least $100,000 from the first published value.",
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
		Pattern:   "The contract notice was published more than 42 days after the contract’s start date.",
		Threshold: "Publication date more than 42 days after the start date.",
		Context:   "The Commonwealth Procurement Rules require agencies to report contracts on AusTender within 42 days of entering into them.",
		Innocent: []string{
			"A start date set earlier than signing, to cover work under an earlier arrangement.",
			"Administrative delay in reporting.",
		},
	},
	{
		ID:        "threshold-cluster",
		Title:     "Several contracts just under $80,000",
		Pattern:   "The same agency awarded the same supplier several contracts valued just below $80,000 in one financial year.",
		Threshold: "Three or more contracts between $72,000 and $80,000 from one agency to one supplier, published in the same financial year.",
		Context:   "For non-corporate Commonwealth entities, open tender is generally required from $80,000. Splitting a requirement to stay under a threshold is not permitted.",
		Innocent: []string{
			"Separate, genuine requirements that happen to be similar in size.",
			"Call-offs from a standing offer or panel with fixed engagement sizes.",
			"Corporate entities, whose open-tender threshold is $400,000.",
		},
	},
	{
		ID:        "repeat-limited",
		Title:     "Repeated limited tenders to one supplier",
		Pattern:   "The same agency awarded the same supplier several contracts by limited tender in one financial year.",
		Threshold: "Five or more limited-tender contracts worth $1 million or more in total, published in the same financial year. Contracts under Appendix A exemptions (such as legal counsel) are not counted.",
		Context:   "Limited tender is allowed in defined circumstances, such as urgency or a single capable supplier. Repeated use with one supplier reduces competition.",
		Innocent: []string{
			"A specialised supplier with no practical alternative.",
			"Additional deliveries that must match existing equipment or systems.",
			"Urgent requirements, such as disaster response.",
		},
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
