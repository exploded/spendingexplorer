// Command load-subfunctions loads a table of expenses below the function level
// into wog_subfunction: Budget Paper No. 1 Appendix A (expenses by function and
// sub-function), or Budget Paper No. 3 rows that itemise one sub-function.
//
// The CSV has function, parent and subfunction columns, then one column per
// fiscal year in $m (a header such as "2024-25 actual" marks actuals). An
// optional "table" column overrides -table for its row. Notes for each row come
// from data/subfunction_notes.csv.
//
// Sub-functions must add up to the BP1 function totals, and rows whose parent
// is in the same file must add up to it, within rounding. Rows that itemise a
// row from another document are not forced to add up; the gap is printed and
// the site shows it.
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
	file := flag.String("file", "", "sub-function CSV")
	budget := flag.String("budget", "", "budget year of the publication, e.g. 2026-27")
	url := flag.String("url", "", "where the source document was downloaded from")
	title := flag.String("title", "", "source document title")
	table := flag.String("table", "", "source table, e.g. 'Table A.6.1'")
	docType := flag.String("doc-type", "bp1_subfunction", "source_document doc_type")
	notesPath := flag.String("notes", "data/subfunction_notes.csv", "function,subfunction,note CSV")
	flag.Parse()
	if *file == "" || *budget == "" || *url == "" || *title == "" || *table == "" {
		log.Fatal("usage: load-subfunctions -file F -budget YYYY-YY -url URL -title T -table T [-doc-type D] [-db spending.db]")
	}

	years, rows, err := readTable(*file, *table)
	if err != nil {
		log.Fatal(err)
	}
	notes, err := readNotes(*notesPath)
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

	docID, err := source.Register(ctx, q, *file, *title, "Commonwealth Treasury", *url, *docType, *budget)
	if err != nil {
		log.Fatal(err)
	}
	if err := q.DeleteWogSubfunctionsForDocType(ctx, db.DeleteWogSubfunctionsForDocTypeParams{DocType: *docType, BudgetYear: *budget}); err != nil {
		log.Fatal(err)
	}
	written := 0
	for pos, r := range rows {
		for i, y := range years {
			et := pbs.EstimateType(*budget, y.fy)
			if y.actual {
				et = "actual"
			}
			if err := q.InsertWogSubfunction(ctx, db.InsertWogSubfunctionParams{
				SourceDocumentID: docID,
				SourceTable:      r.table,
				FiscalYear:       y.fy,
				EstimateType:     et,
				FunctionName:     r.function,
				Parent:           r.parent,
				Name:             r.name,
				Position:         int64(pos),
				Amount:           r.amounts[i],
				Note:             notes[r.function+"|"+r.name],
			}); err != nil {
				log.Fatalf("%s / %s %s: %v", r.function, r.name, y.fy, err)
			}
			written++
		}
	}
	if err := check(ctx, q, *budget, docID); err != nil {
		log.Fatal(err)
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-subfunctions", SourceDocumentID: docID, RowsRead: int64(len(rows)), RowsWritten: int64(written),
	}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d rows, %d amounts\n", len(rows), written)
}

// rounding allows $1m per row added, since the tables are rounded to $1m.
const rounding = 1_000_000

// check compares each group of rows with the total it sits inside, across
// every document loaded for the budget year.
func check(ctx context.Context, q *db.Queries, budget string, docID int64) error {
	fns, err := q.FunctionAmounts(ctx, budget)
	if err != nil {
		return err
	}
	subs, err := q.SubfunctionAmounts(ctx, budget)
	if err != nil {
		return err
	}
	type key struct{ fy, function, name string }
	type group struct {
		sum      int64
		rows     int64
		sameDoc  bool // the group's rows and its parent come from one document
		fromThis bool // the group includes rows from the document just loaded
	}
	parents := map[key]int64{}   // sub-function amounts
	parentDoc := map[key]int64{} // and their document
	for _, s := range subs {
		k := key{s.FiscalYear, s.FunctionName, s.Name}
		parents[k] = s.Amount
		parentDoc[k] = s.SourceDocumentID
	}
	groups := map[key]*group{}
	for _, s := range subs {
		k := key{s.FiscalYear, s.FunctionName, s.Parent}
		g := groups[k]
		if g == nil {
			g = &group{sameDoc: true}
			groups[k] = g
		}
		g.sum += s.Amount
		g.rows++
		g.fromThis = g.fromThis || s.SourceDocumentID == docID
		if s.Parent != "" && parentDoc[k] != s.SourceDocumentID {
			g.sameDoc = false
		}
	}
	totals := map[key]int64{}
	for _, f := range fns {
		totals[key{f.FiscalYear, f.Category, ""}] = f.Amount
	}

	var errs []string
	for k, g := range groups {
		if !g.fromThis {
			continue
		}
		var want int64
		var ok bool
		if k.name == "" {
			want, ok = totals[k]
			if !ok {
				continue // a year the expenses-by-function table doesn't cover
			}
		} else if want, ok = parents[k]; !ok {
			errs = append(errs, fmt.Sprintf("%s %s: parent %q not loaded", k.fy, k.function, k.name))
			continue
		}
		diff := want - g.sum
		switch {
		case !g.sameDoc:
			fmt.Printf("%s %s / %s: itemised %s of %s, gap %s\n", k.fy, k.function, k.name, millions(g.sum), millions(want), millions(diff))
		case abs(diff) > g.rows*rounding:
			label := k.function
			if k.name != "" {
				label += " / " + k.name
			}
			errs = append(errs, fmt.Sprintf("%s %s: rows add to %s, total is %s", k.fy, label, millions(g.sum), millions(want)))
		}
	}
	if errs != nil {
		return fmt.Errorf("totals don't match:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

type year struct {
	fy     string
	actual bool
}

type row struct {
	function, parent, name, table string
	amounts                       []int64 // whole dollars, aligned with years
}

func readTable(path, defaultTable string) ([]year, []row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	recs, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(recs) < 2 {
		return nil, nil, fmt.Errorf("%s: no data rows", path)
	}
	head := recs[0]
	if len(head) < 4 || head[0] != "function" || head[1] != "parent" || head[2] != "subfunction" {
		return nil, nil, fmt.Errorf("%s: header must start function,parent,subfunction", path)
	}
	var years []year
	var cols []int
	tableCol := -1
	for i, h := range head[3:] {
		if h == "table" {
			tableCol = i + 3
			continue
		}
		fy := yearRe.FindString(h)
		if fy == "" {
			return nil, nil, fmt.Errorf("%s: column %q is not a fiscal year", path, h)
		}
		years = append(years, year{fy: fy, actual: strings.Contains(strings.ToLower(h), "actual")})
		cols = append(cols, i+3)
	}
	var out []row
	for n, rec := range recs[1:] {
		rw := row{function: rec[0], parent: rec[1], name: rec[2], table: defaultTable}
		if tableCol >= 0 && rec[tableCol] != "" {
			rw.table = rec[tableCol]
		}
		for _, c := range cols {
			v, err := strconv.ParseFloat(strings.ReplaceAll(rec[c], ",", ""), 64)
			if err != nil {
				return nil, nil, fmt.Errorf("%s line %d: %v", path, n+2, err)
			}
			rw.amounts = append(rw.amounts, int64(math.Round(v*1e6)))
		}
		out = append(out, rw)
	}
	return years, out, nil
}

// readNotes reads function,subfunction,note rows keyed by "function|subfunction".
func readNotes(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := map[string]string{}
	for _, r := range recs[1:] {
		m[r[0]+"|"+r[1]] = r[2]
	}
	return m, nil
}

func millions(v int64) string { return strconv.FormatFloat(float64(v)/1e6, 'f', 1, 64) + "m" }

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
