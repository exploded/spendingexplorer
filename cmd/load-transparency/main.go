// Command load-transparency loads the contract spending entities reported in
// their annual reports, exported from the Transparency Portal's Data Explorer
// into data/raw/transparency (see README). It replaces all earlier rows.
//
// Each figure is checked against the entity's Budget expenses; figures that
// fail keep their published value and the reason, and are never summed.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
	"spendingexplorer/internal/transparency"
)

const (
	publisher = "Department of Finance, Transparency Portal"
	portalURL = "https://www.transparency.gov.au/"
)

// Export file names start with the data set's title.
var files = []struct {
	prefix string
	kind   transparency.Kind
	orgs   bool
}{
	{"Data set for Expenditure on Reportable Consultancy Contracts", transparency.Consultancy, false},
	{"Data set for Expenditure on Reportable Non-Consultancy Contracts", transparency.NonConsultancy, false},
	{"Data set for Organisations Receiving a Share of Reportable Consultancy", transparency.Consultancy, true},
	{"Data set for Organisations Receiving a Share of Reportable Non-Consultancy", transparency.NonConsultancy, true},
}

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	dir := flag.String("dir", "data/raw/transparency", "folder holding the Data Explorer exports")
	aliases := flag.String("aliases", "data/agency_aliases.csv", "reviewed agency -> PBS entity names")
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	if err := load(context.Background(), conn, *dir, *aliases); err != nil {
		log.Fatal(err)
	}
}

func load(ctx context.Context, conn *sql.DB, dir, aliasFile string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := db.New(tx)

	match, err := entityMatcher(ctx, q, aliasFile)
	if err != nil {
		return err
	}
	ceilings := map[int64]int64{}
	rows, err := q.EntityExpenseCeilings(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		ceilings[r.EntityID] = r.Expenses
	}
	isPerson, err := personTest(ctx, q)
	if err != nil {
		return err
	}
	if err := q.ClearReportedSpend(ctx); err != nil {
		return err
	}
	if err := q.ClearReportedOrgs(ctx); err != nil {
		return err
	}

	// Each total and its check result, for checking organisations' shares.
	totals := map[key]checked{}
	unmatched := map[string]bool{}
	entityOf := func(name string) sql.NullInt64 {
		id, ok, excluded := match(name)
		if !ok && !excluded {
			unmatched[name] = true
		}
		return sql.NullInt64{Int64: id, Valid: ok}
	}

	// Spending totals first, then the organisations checked against them.
	for _, wantOrgs := range []bool{false, true} {
		for _, f := range files {
			if f.orgs != wantOrgs {
				continue
			}
			path, err := find(dir, f.prefix)
			if err != nil {
				return err
			}
			title := "Transparency Portal: " + strings.TrimPrefix(strings.TrimSuffix(filepath.Base(path), ".csv"), "Data set for ")
			docID, err := source.Register(ctx, q, path, title, publisher, portalURL, "transparency", "")
			if err != nil {
				return err
			}
			r, err := os.Open(path)
			if err != nil {
				return err
			}
			n, flagged, err := loadFile(ctx, q, r, f.kind, f.orgs, docID, entityOf, ceilings, totals, isPerson)
			r.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
				Command: "load-transparency", SourceDocumentID: docID, RowsRead: int64(n), RowsWritten: int64(n),
				Notes: fmt.Sprintf("%d figures fail a consistency check", flagged),
			}); err != nil {
				return err
			}
			log.Printf("%s: %d rows, %d fail a consistency check", title, n, flagged)
		}
	}
	for name := range unmatched {
		log.Printf("  unmatched entity: %s", name)
	}
	spends, err := q.UnlinkAbsorbedSpend(ctx)
	if err != nil {
		return err
	}
	orgs, err := q.UnlinkAbsorbedOrgs(ctx)
	if err != nil {
		return err
	}
	log.Printf("unlinked %d totals and %d organisations of bodies reported separately before being absorbed", spends, orgs)
	return tx.Commit()
}

// key identifies one entity's figure for a year, as published.
type key struct {
	entity, year string
	kind         transparency.Kind
}

// checked is a reported total and why it looks wrong, if it does.
type checked struct {
	amount int64
	note   string
}

// loadFile is called with spend files before organisation files, so totals
// holds every entity's checked totals.
func loadFile(ctx context.Context, q *db.Queries, r io.Reader, kind transparency.Kind, orgs bool, docID int64,
	entityOf func(string) sql.NullInt64, ceilings map[int64]int64, totals map[key]checked,
	isPerson func(name, abn string) bool) (n, flagged int, err error) {
	if !orgs {
		spends, err := transparency.ParseSpend(r, kind)
		if err != nil {
			return 0, 0, err
		}
		for _, s := range spends {
			ent := entityOf(s.Entity)
			note := transparency.CheckSpend(s, ceilings[ent.Int64])
			totals[key{s.Entity, s.Year, kind}] = checked{s.Amount, note}
			if note != "" {
				flagged++
			}
			if err := q.InsertReportedSpend(ctx, db.InsertReportedSpendParams{
				EntityName: s.Entity, EntityID: ent, ReportingYear: s.Year, Kind: string(kind),
				NewContracts: s.NewContracts, NewAmount: s.NewAmount, OngoingContracts: s.OngoingContracts,
				OngoingAmount: s.OngoingAmount, Contracts: s.Contracts, Amount: s.Amount,
				CheckNote: note, SourceDocumentID: docID,
			}); err != nil {
				return 0, 0, err
			}
		}
		return len(spends), flagged, nil
	}
	list, err := transparency.ParseOrgs(r, kind)
	if err != nil {
		return 0, 0, err
	}
	for _, o := range list {
		ent := entityOf(o.Entity)
		t := totals[key{o.Entity, o.Year, kind}]
		note := transparency.CheckOrg(o, ceilings[ent.Int64], t.amount, t.note)
		if note != "" {
			flagged++
		}
		var individual int64
		if isPerson(o.Name, o.ABN) {
			individual = 1
		}
		if err := q.InsertReportedOrg(ctx, db.InsertReportedOrgParams{
			EntityName: o.Entity, EntityID: ent, ReportingYear: o.Year, Kind: string(kind),
			OrgName: o.Name, OrgAbn: o.ABN, Amount: o.Amount, Individual: individual,
			CheckNote: note, SourceDocumentID: docID,
		}); err != nil {
			return 0, 0, err
		}
	}
	return len(list), flagged, nil
}

// find returns the one export in dir whose name starts with prefix.
func find(dir, prefix string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, prefix+"*.csv"))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("want one file named %q… in %s, found %d", prefix, dir, len(matches))
	}
	return matches[0], nil
}

// entityMatcher matches published entity names to entities by exact or
// normalised name, including earlier PBS names, AusTender agency names and
// the reviewed agency aliases.
// An alias with no entity marks a name left unmatched on purpose.
func entityMatcher(ctx context.Context, q *db.Queries, aliasFile string) (func(string) (int64, bool, bool), error) {
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
	// The portal adds abbreviations and footnote markers, as in "Australian
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

// personTest reports whether an organisation is, or may be, a person: the
// ABR records its ABN as an individual's, AusTender's supplier with that ABN
// is, or, with no ABR record, the name reads as a person's.
func personTest(ctx context.Context, q *db.Queries) (func(name, abn string) bool, error) {
	types := map[string]string{}
	rows, err := q.ABREntityTypes(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		types[r.Abn] = r.EntityType
	}
	individuals := map[string]bool{}
	abns, err := q.IndividualSupplierABNs(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range abns {
		individuals[a] = true
	}
	names, err := q.ListGivenNames(ctx)
	if err != nil {
		return nil, err
	}
	given := make(map[string]bool, len(names))
	for _, n := range names {
		given[n] = true
	}
	return func(name, abn string) bool {
		if t, ok := types[abn]; ok && abn != "" {
			return t == "IND"
		}
		if individuals[abn] {
			return true
		}
		return austender.LooksLikePerson(name, given)
	}, nil
}
