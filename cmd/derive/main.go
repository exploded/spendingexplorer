// Command derive rebuilds derived tables from loaded data: each contract's
// value spread across financial years (contract_fy), and the "worth a closer
// look" signals (signal). Run it after any loader.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/fiscal"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
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

	// Signals: each rule in internal/signals has one derivation query.
	if err := q.ClearSignals(ctx); err != nil {
		log.Fatal(err)
	}
	for _, derive := range []func(context.Context) error{
		q.DeriveValueIncrease, q.DeriveLatePublication, q.DeriveThresholdCluster, q.DeriveRepeatLimited,
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
