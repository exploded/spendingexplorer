// Command reconcile compares the sum of PBS line items with the Budget Paper No. 1
// total expenses for the same budget, and prints where the difference comes from.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/pbs"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	top := flag.Int("top", 10, "number of largest transfers to list")
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	q := db.New(conn)

	docs, err := q.ListPBSDocuments(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range docs {
		prev := previousYear(d.BudgetYear)
		for _, fy := range []string{prev, d.BudgetYear} {
			if err := report(ctx, q, d, fy, *top); err != nil {
				log.Fatal(err)
			}
		}
	}
}

func report(ctx context.Context, q *db.Queries, d db.ListPBSDocumentsRow, fy string, top int) error {
	fmt.Printf("\n== %s, fiscal year %s (%s)\n", d.Title, fy, pbs.EstimateType(d.BudgetYear, fy))

	sums, err := q.SumByTreatment(ctx, db.SumByTreatmentParams{SourceDocumentID: d.ID, FiscalYear: fy})
	if err != nil {
		return err
	}
	var gross, expense int64
	for _, s := range sums {
		fmt.Printf("  %-9s %-32s %8s  %5d lines", s.Treatment, s.TreatmentRule, bn(s.Total), s.Lines)
		if s.NotPublished > 0 {
			fmt.Printf("  (%d not for publication)", s.NotPublished)
		}
		fmt.Println()
		if s.Treatment != "subtotal" {
			gross += s.Total
		}
		if s.Treatment == "expense" {
			expense += s.Total
		}
	}
	fmt.Printf("  %-42s %8s\n", "gross line items", bn(gross))
	fmt.Printf("  %-42s %8s\n", "itemised (after transfers removed)", bn(expense))

	w, err := q.WogTotalForYear(ctx, db.WogTotalForYearParams{BudgetYear: d.BudgetYear, FiscalYear: fy})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fmt.Println("  no BP1 total loaded for this budget")
	case err != nil:
		return err
	default:
		gap := w.Amount - expense
		fmt.Printf("  %-42s %8s\n", "BP1 total expenses", bn(w.Amount))
		fmt.Printf("  %-42s %8s  (%.1f%% of BP1)\n", "whole-of-government adjustments", bn(gap), 100*float64(gap)/float64(w.Amount))
	}

	big, err := q.LargestTransfers(ctx, db.LargestTransfersParams{SourceDocumentID: d.ID, FiscalYear: fy, Limit: int64(top)})
	if err != nil {
		return err
	}
	if len(big) > 0 {
		fmt.Println("  largest transfers removed:")
		for _, b := range big {
			fmt.Printf("    %8s  %s – %s\n", bn(b.Amount.Int64), b.Entity, b.LineItem)
		}
	}

	ports, err := q.SumExpenseByPortfolio(ctx, db.SumExpenseByPortfolioParams{SourceDocumentID: d.ID, FiscalYear: fy})
	if err != nil {
		return err
	}
	fmt.Println("  itemised by portfolio:")
	for _, p := range ports {
		fmt.Printf("    %8s  %s\n", bn(p.Total), p.Portfolio)
	}
	return nil
}

func previousYear(fy string) string {
	var a, b int
	fmt.Sscanf(fy, "%d-%d", &a, &b)
	return fmt.Sprintf("%d-%02d", a-1, (b+99)%100)
}

func bn(dollars int64) string {
	return fmt.Sprintf("$%.1fbn", float64(dollars)/1e9)
}
