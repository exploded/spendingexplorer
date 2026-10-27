// Command load-pbs loads a PBS Program Expense Line Items file into SQLite.
// Re-running it for the same file replaces that file's rows.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/pbs"
	"spendingexplorer/internal/source"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	file := flag.String("file", "", "line-items CSV or XLSX")
	budget := flag.String("budget", "", "budget year of the publication, e.g. 2025-26")
	url := flag.String("url", "", "where the file was downloaded from")
	title := flag.String("title", "", "source document title")
	flag.Parse()
	if *file == "" || *budget == "" || *url == "" {
		log.Fatal("usage: load-pbs -file F -budget YYYY-YY -url URL [-title T] [-db spending.db]")
	}
	if *title == "" {
		*title = *budget + " Portfolio Budget Statements – Program Expense Line Items"
	}

	f, err := pbs.ReadFile(*file)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()

	written, err := load(ctx, conn, f, *file, *budget, *url, *title)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d lines, %d amounts, years %v\n", len(f.Lines), written, f.Years)
}

func load(ctx context.Context, conn *sql.DB, f *pbs.File, path, budget, url, title string) (int, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := db.New(tx)

	docID, err := source.Register(ctx, q, path, title, "Department of Finance via data.gov.au", url, "pbs_line_items", budget)
	if err != nil {
		return 0, err
	}
	if err := q.DeleteProgramExpensesForDoc(ctx, docID); err != nil {
		return 0, err
	}

	entities := map[string]int64{}
	written := 0
	for _, l := range f.Lines {
		entID, ok := entities[l.Entity]
		if !ok {
			if entID, err = q.UpsertEntity(ctx, l.Entity); err != nil {
				return 0, err
			}
			entities[l.Entity] = entID
		}
		progID, err := q.UpsertProgram(ctx, db.UpsertProgramParams{EntityID: entID, BudgetYear: budget, Portfolio: l.Portfolio, Outcome: l.Outcome, Name: l.Program})
		if err != nil {
			return 0, err
		}
		t := pbs.Classify(l)
		for _, v := range l.Values {
			p := db.InsertProgramExpenseParams{
				ProgramID:        progID,
				SourceDocumentID: docID,
				SourceTable:      l.SourceTable,
				SourceRow:        int64(l.Row),
				FiscalYear:       v.FiscalYear,
				EstimateType:     pbs.EstimateType(budget, v.FiscalYear),
				ExpenseKind:      l.Kind,
				LineItem:         l.LineItem,
				Treatment:        t.Kind,
				TreatmentRule:    t.RuleID,
			}
			if v.NotPublished {
				p.NotPublished = 1
			} else {
				p.Amount = sql.NullInt64{Int64: v.Dollars, Valid: true}
			}
			if err := q.InsertProgramExpense(ctx, p); err != nil {
				return 0, err
			}
			written++
		}
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-pbs", SourceDocumentID: docID, RowsRead: int64(len(f.Lines)), RowsWritten: int64(written),
	}); err != nil {
		return 0, err
	}
	return written, tx.Commit()
}
