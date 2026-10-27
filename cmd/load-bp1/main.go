// Command load-bp1 loads Budget Paper No. 1 "Estimates of expenses by function"
// (Table 5.3 in 2025-26, Table 6.3 in 2026-27) into wog_expense. Amounts are $m.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/pbs"
	"spendingexplorer/internal/source"
)

var yearRe = regexp.MustCompile(`\d{4}-\d{2}`)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	file := flag.String("file", "", "BP1 expenses-by-function CSV")
	budget := flag.String("budget", "", "budget year of the publication, e.g. 2025-26")
	url := flag.String("url", "", "where the file was downloaded from")
	title := flag.String("title", "", "source document title")
	flag.Parse()
	if *file == "" || *budget == "" || *url == "" {
		log.Fatal("usage: load-bp1 -file F -budget YYYY-YY -url URL [-title T] [-db spending.db]")
	}
	if *title == "" {
		*title = *budget + " Budget Paper No. 1 – Estimates of expenses by function"
	}

	years, rows, err := readTable(*file)
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

	docID, err := source.Register(ctx, q, *file, *title, "Commonwealth Treasury via data.gov.au", *url, "bp1_function", *budget)
	if err != nil {
		log.Fatal(err)
	}
	if err := q.DeleteWogExpensesForDoc(ctx, docID); err != nil {
		log.Fatal(err)
	}
	written := 0
	for _, r := range rows {
		for i, v := range r.amounts {
			if err := q.InsertWogExpense(ctx, db.InsertWogExpenseParams{
				SourceDocumentID: docID,
				FiscalYear:       years[i],
				EstimateType:     pbs.EstimateType(*budget, years[i]),
				Lens:             "function",
				Category:         r.category,
				Amount:           v,
			}); err != nil {
				log.Fatal(err)
			}
			written++
		}
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-bp1", SourceDocumentID: docID, RowsRead: int64(len(rows)), RowsWritten: int64(written),
	}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d functions, %d amounts, years %v\n", len(rows), written, years)
}

type row struct {
	category string
	amounts  []int64 // whole dollars, aligned with years
}

// readTable finds the header row holding the fiscal years, then reads every
// following row whose first cell is a label and whose cells are all numeric.
func readTable(path string) ([]string, []row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}

	var years []string
	var cols []int
	var out []row
	for _, rec := range recs {
		if years == nil {
			for i, cell := range rec {
				if y := yearRe.FindString(cell); y != "" {
					years = append(years, y)
					cols = append(cols, i)
				}
			}
			continue
		}
		label := strings.TrimSpace(strings.TrimPrefix(rec[0], "\ufeff"))
		if label == "" {
			continue
		}
		amounts := make([]int64, 0, len(cols))
		for _, c := range cols {
			if c >= len(rec) {
				break
			}
			v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(rec[c]), ",", ""), 64)
			if err != nil {
				break
			}
			amounts = append(amounts, int64(math.Round(v*1e6)))
		}
		if len(amounts) != len(cols) {
			continue
		}
		out = append(out, row{category: label, amounts: amounts})
	}
	if years == nil || len(out) == 0 {
		return nil, nil, fmt.Errorf("%s: no year header or data rows found", path)
	}
	return years, out, nil
}
