// Command derive rebuilds derived tables from loaded data: each contract's
// value spread across financial years (contract_fy), suppliers who may be
// individuals (supplier_individual), corporate entities from the reviewed list
// (corporate_agency), and the "worth a closer look" signals (signal). Run it
// after any loader.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/fiscal"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	corporate := flag.String("corporate", "data/corporate_agencies.csv", "reviewed AusTender agencies that are corporate Commonwealth entities")
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback()
	q := db.New(tx)

	contracts, err := q.ContractsForApportion(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if err := q.ClearContractFY(ctx); err != nil {
		log.Fatal(err)
	}
	rows, skipped := 0, 0
	for _, c := range contracts {
		start := c.StartDate
		if start == "" {
			start = c.PublishedDate
		}
		shares, err := fiscal.Apportion(c.Value, start, c.EndDate)
		if err != nil {
			skipped++
			continue
		}
		for _, s := range shares {
			if err := q.InsertContractFY(ctx, db.InsertContractFYParams{CnID: c.CnID, FiscalYear: s.FiscalYear, Amount: s.Amount}); err != nil {
				log.Fatal(err)
			}
			rows++
		}
	}
	fmt.Printf("contract_fy: %d contracts, %d year rows, %d skipped (no usable dates)\n", len(contracts), rows, skipped)

	// Suppliers who are, or may be, individuals: registered as individuals in
	// the ABR, or with no ABR record and a name that reads as a person's.
	if err := q.ClearSupplierIndividuals(ctx); err != nil {
		log.Fatal(err)
	}
	if err := q.DeriveABRIndividuals(ctx); err != nil {
		log.Fatal(err)
	}
	names, err := q.ListGivenNames(ctx)
	if err != nil {
		log.Fatal(err)
	}
	given := make(map[string]bool, len(names))
	for _, n := range names {
		given[n] = true
	}
	unregistered, err := q.SuppliersWithoutABR(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range unregistered {
		if austender.LooksLikePerson(s.Name, given) {
			if err := q.InsertSupplierIndividual(ctx, db.InsertSupplierIndividualParams{SupplierID: s.ID, Basis: "name"}); err != nil {
				log.Fatal(err)
			}
		}
	}
	inds, err := q.SupplierIndividualCounts(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range inds {
		fmt.Printf("individual suppliers (%s): %d\n", c.Basis, c.Suppliers)
	}

	// Corporate entities have a higher open-tender threshold; the contract_pub
	// view reads this table, so it must be loaded before the signals.
	if err := loadCorporate(ctx, q, *corporate); err != nil {
		log.Fatal(err)
	}

	// Signals: each rule in internal/signals has one derivation query, except
	// late-batch, which takes batched matches from late-publication.
	if err := q.ClearSignals(ctx); err != nil {
		log.Fatal(err)
	}
	for _, derive := range []func(context.Context) error{
		q.DeriveValueIncrease, q.DeriveLatePublication, q.DeriveLateBatch, q.RemoveBatchedLate,
		q.DeriveThresholdCluster, q.DeriveRepeatLimited, q.DeriveCrossedThreshold, q.DeriveNewABN,
	} {
		if err := derive(ctx); err != nil {
			log.Fatal(err)
		}
	}
	counts, err := q.SignalCounts(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range counts {
		fmt.Printf("signal %-18s %7d contracts  $%.1fm\n", c.RuleID, c.Contracts, float64(c.Amount)/1e6)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
}

// loadCorporate replaces corporate_agency with the agencies in the reviewed
// CSV (columns: agency, flipchart_name). Names with no loaded agency are
// reported, since the list should track AusTender's agency names.
func loadCorporate(ctx context.Context, q *db.Queries, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := q.ClearCorporateAgencies(ctx); err != nil {
		return err
	}
	matched := 0
	for _, rec := range records[1:] {
		n, err := q.InsertCorporateAgency(ctx, rec[0])
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Printf("corporate agency not loaded: %s\n", rec[0])
		}
		matched += int(n)
	}
	fmt.Printf("corporate agencies: %d of %d\n", matched, len(records)-1)
	return nil
}
