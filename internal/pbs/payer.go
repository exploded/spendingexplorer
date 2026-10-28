package pbs

import "strings"

// PayerLine is an administered expense line in a portfolio that may be the
// payment behind a corporate entity's funding line.
type PayerLine struct {
	LineItem string
	Amount   int64
}

// MatchPayers finds, for each of a corporate entity's funding amounts, the
// administered line that pays it, or -1 when none can be told apart. The PBS
// does not link the two, so a line matches only when its amount is within
// 0.05% (at least $1,000, as amounts are published in thousands) and it is
// plainly about this payment: it mentions corporate entities or the entity by
// name, or the amount is $1bn or more. Smaller unnamed lines match by
// coincidence too often (a $5.0m Antarctic science line matched the Clean
// Energy Finance Corporation). Each line pays at most one funding amount, and
// two candidates mean no match.
func MatchPayers(entity string, funding []int64, lines []PayerLine) []int {
	name := strings.ToLower(entity)
	used := make([]bool, len(lines))
	out := make([]int, len(funding))
	for i, amt := range funding {
		out[i] = -1
		tol := max(amt/2000, 1000)
		found := 0
		for j, l := range lines {
			if used[j] || l.Amount < amt-tol || l.Amount > amt+tol {
				continue
			}
			item := strings.ToLower(l.LineItem)
			if !strings.Contains(item, "corporate") && !strings.Contains(item, name) && amt < 1e9 {
				continue
			}
			if found++; found == 1 {
				out[i] = j
			}
		}
		if found != 1 {
			out[i] = -1
			continue
		}
		used[out[i]] = true
	}
	return out
}
