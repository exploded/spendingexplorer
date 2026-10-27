// Package entitymatch matches the entity names other publications use
// (Transparency Portal, GrantConnect) to PBS entities.
package entitymatch

import (
	"context"
	"regexp"
	"strings"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

// Func returns the entity a published name belongs to. excluded is true when
// the reviewed alias file leaves the name unmatched on purpose.
type Func func(name string) (id int64, ok, excluded bool)

// New matches by exact or normalised name, including earlier PBS names,
// AusTender agency names and the reviewed agency aliases (aliasFile). An
// alias with no entity marks a name left unmatched on purpose.
func New(ctx context.Context, q *db.Queries, aliasFile string) (Func, error) {
	byKey := map[string]int64{}
	agencies, err := q.ListAgencyEntities(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range agencies {
		byKey[austender.NameKey(a.Name)] = a.EntityID.Int64
	}
	pbsNames, err := q.ListEntityAliases(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range pbsNames {
		byKey[austender.NameKey(a.Alias)] = a.EntityID
	}
	ents, err := q.ListEntities(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]int64{}
	for _, e := range ents {
		byName[e.Name] = e.ID
		byKey[austender.NameKey(e.Name)] = e.ID
	}
	aliases, err := source.ReadNames(aliasFile)
	if err != nil {
		return nil, err
	}
	manual := map[string]string{} // by normalised name
	for name, entity := range aliases {
		manual[austender.NameKey(name)] = entity
	}
	// Publications add abbreviations and footnote markers, as in "Australian
	// Securities and Investments Commission (ASIC)" or "Department of
	// Education [i]"; a name in brackets may also be the full name.
	return func(name string) (id int64, ok, excluded bool) {
		short := strings.TrimSpace(brackets.ReplaceAllString(name, ""))
		inner := ""
		if m := bracketed.FindStringSubmatch(name); m != nil {
			inner = m[1]
		}
		for _, n := range []string{name, short, inner} {
			if n == "" {
				continue
			}
			if entity, listed := manual[austender.NameKey(n)]; listed {
				if entity == "" {
					return 0, false, true
				}
				if id, ok := byName[entity]; ok {
					return id, true, false
				}
			}
			if id, ok := byKey[austender.NameKey(n)]; ok {
				return id, true, false
			}
		}
		return 0, false, false
	}, nil
}

var (
	brackets  = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]`)
	bracketed = regexp.MustCompile(`\(([^)]{12,})\)`) // a full name, not an abbreviation
)
