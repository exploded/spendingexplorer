// Command load-grants loads GrantConnect Grant Award List exports from
// data/raw/grantconnect (see README) into grant_award, and spreads each
// award's value over its term in grant_fy. It replaces all earlier rows and
// does not touch tables other loaders or derive own.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/entitymatch"
	"spendingexplorer/internal/fiscal"
	"spendingexplorer/internal/grantconnect"
	"spendingexplorer/internal/source"
)

const (
	publisher = "Department of Finance, GrantConnect"
	siteURL   = "https://www.grants.gov.au/Ga/List"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	dir := flag.String("dir", "data/raw/grantconnect", "folder holding the Grant Award List exports")
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
	paths, err := filepath.Glob(filepath.Join(dir, "*.xlsx"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no .xlsx exports in %s", dir)
	}
	sort.Strings(paths)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := db.New(tx)

	// Read every export; remember which document each published record came from.
	var files []*grantconnect.File
	docOf := map[string]int64{}
	for _, p := range paths {
		f, err := grantconnect.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		docID, err := source.Register(ctx, q, p, "GrantConnect: Grant Award List, published "+f.Range, publisher, siteURL, "grantconnect", "")
		if err != nil {
			return err
		}
		note := ""
		switch {
		case f.Capped:
			note = "skipped: the search returned more than 50,000 awards, so the export is incomplete"
		case f.Repeated > 0:
			note = fmt.Sprintf("%d rows repeat an award already in the file (GrantConnect export fault); counted once", f.Repeated)
		}
		log.Printf("%s %-28s %6d awards %s", filepath.Base(p), f.Range, len(f.Awards), note)
		if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
			Command: "load-grants", SourceDocumentID: docID, RowsRead: int64(len(f.Awards) + f.Repeated),
			RowsWritten: int64(len(f.Awards)), Notes: note,
		}); err != nil {
			return err
		}
		if f.Capped {
			continue
		}
		for _, a := range f.Awards {
			docOf[a.ID] = docID
		}
		files = append(files, f)
	}
	awards := grantconnect.Merge(files)

	match, err := entitymatch.New(ctx, q, aliasFile)
	if err != nil {
		return err
	}
	given, err := givenNames(ctx, q)
	if err != nil {
		return err
	}
	if err := q.ClearGrantFY(ctx); err != nil {
		return err
	}
	if err := q.ClearGrantAwards(ctx); err != nil {
		return err
	}

	unmatched := map[string]int64{}
	var years, undated int
	for _, a := range awards {
		id, ok, excluded := match(a.Agency)
		if !ok && !excluded {
			unmatched[a.Agency] += a.Value
		}
		var individual, unnamed int64
		if a.Aggregate || placeholder[strings.ToLower(a.Recipient)] {
			unnamed = 1
		} else if austender.LooksLikePerson(a.Recipient, given) {
			individual = 1
		}
		if err := q.InsertGrantAward(ctx, db.InsertGrantAwardParams{
			GaID: a.Base, VersionID: a.ID, Activity: a.Activity, AgencyName: a.Agency,
			EntityID: sql.NullInt64{Int64: id, Valid: ok}, PublishDate: a.PublishDate, Category: a.Category,
			StartDate: a.StartDate, EndDate: a.EndDate, Value: a.Value, GoID: a.GOID, Recipient: a.Recipient,
			Individual: individual, Unnamed: unnamed, OneOff: boolInt(a.OneOff), Aggregate: boolInt(a.Aggregate),
			SourceDocumentID: docOf[a.ID],
		}); err != nil {
			return fmt.Errorf("%s: %w", a.ID, err)
		}
		start := a.StartDate
		if start == "" {
			start = a.PublishDate
		}
		shares, err := fiscal.Apportion(a.Value, start, a.EndDate)
		if err != nil {
			undated++
			continue
		}
		for _, s := range shares {
			if err := q.InsertGrantFY(ctx, db.InsertGrantFYParams{GaID: a.Base, FiscalYear: s.FiscalYear, Amount: s.Amount}); err != nil {
				return err
			}
			years++
		}
	}
	log.Printf("grant_award: %d awards; grant_fy: %d year rows, %d awards without usable dates", len(awards), years, undated)

	names := make([]string, 0, len(unmatched))
	for n := range unmatched {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return unmatched[names[i]] > unmatched[names[j]] })
	for _, n := range names {
		log.Printf("  unmatched agency: %s ($%.1fm)", n, float64(unmatched[n])/1e6)
	}
	return tx.Commit()
}

// placeholder holds recipient names agencies publish when no recipient is
// named, such as for withheld or aggregated awards.
var placeholder = map[string]bool{"multiple": true, "various": true, "n/a": true, "na": true, "not applicable": true}

func givenNames(ctx context.Context, q *db.Queries) (map[string]bool, error) {
	names, err := q.ListGivenNames(ctx)
	if err != nil {
		return nil, err
	}
	given := make(map[string]bool, len(names))
	for _, n := range names {
		given[n] = true
	}
	return given, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
