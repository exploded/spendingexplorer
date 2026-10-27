// Command load-descriptions loads data/program_descriptions_<year>.csv (built
// and reviewed with extract-descriptions) into program_description.
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	year := flag.String("budget", "2026-27", "budget year")
	flag.Parse()
	path := fmt.Sprintf("data/program_descriptions_%s.csv", *year)

	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

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

	docID, err := source.Register(ctx, q, path, *year+" Portfolio Budget Statements – program objectives",
		"Australian Government departments (PBS documents)", "https://budget.gov.au/content/pbs/index.htm", "pbs_descriptions", *year)
	if err != nil {
		log.Fatal(err)
	}
	written, missing := 0, 0
	for _, r := range rows[1:] {
		// budget_year, entity, program, objective, source_url, page
		id, err := q.FindProgram(ctx, db.FindProgramParams{BudgetYear: r[0], Entity: r[1], Program: r[2]})
		if errors.Is(err, sql.ErrNoRows) {
			missing++
			log.Printf("no program %q / %q", r[1], r[2])
			continue
		} else if err != nil {
			log.Fatal(err)
		}
		page, _ := strconv.ParseInt(r[5], 10, 64)
		if err := q.UpsertProgramDescription(ctx, db.UpsertProgramDescriptionParams{
			ProgramID: id, Text: r[3], SourceUrl: r[4], Page: sql.NullInt64{Int64: page, Valid: page > 0}, SourceDocumentID: docID,
		}); err != nil {
			log.Fatal(err)
		}
		written++
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-descriptions", SourceDocumentID: docID, RowsRead: int64(len(rows) - 1), RowsWritten: int64(written),
	}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d descriptions, %d without a matching program\n", written, missing)
}
