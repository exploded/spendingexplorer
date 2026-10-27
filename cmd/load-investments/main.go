// Command load-investments loads data/investments.csv (built and reviewed with
// extract-investments) into policy_investment. Each publication is recorded as
// its own source document, fingerprinted by the PDF's SHA-256.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"strconv"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	file := flag.String("file", "data/investments.csv", "extracted investments CSV")
	flag.Parse()

	f, err := os.Open(*file)
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

	// doc_type, budget_year, url, sha256, title, item, item_key, fiscal_year, estimate_type, amount_m, is_total
	docs := map[string]int64{} // by sha256
	read, written := map[int64]int64{}, map[int64]int64{}
	for _, r := range rows[1:] {
		docID, ok := docs[r[3]]
		if !ok {
			pub := "Commonwealth Treasury"
			if docID, err = source.RegisterHash(ctx, q, r[3], path.Base(r[2]), r[4], pub, r[2], r[0], r[1]); err != nil {
				log.Fatal(err)
			}
			if err := q.DeletePolicyInvestmentsForDoc(ctx, docID); err != nil {
				log.Fatal(err)
			}
			docs[r[3]] = docID
		}
		read[docID]++
		m, err := strconv.ParseInt(r[9], 10, 64)
		if err != nil {
			log.Fatalf("%v: %v", r, err)
		}
		total, _ := strconv.ParseInt(r[10], 10, 64)
		if err := q.InsertPolicyInvestment(ctx, db.InsertPolicyInvestmentParams{
			SourceDocumentID: docID, Item: r[5], ItemKey: r[6], FiscalYear: r[7], EstimateType: r[8],
			Amount: m * 1_000_000, IsTotal: total,
		}); err != nil {
			log.Fatalf("%v: %v", r, err)
		}
		written[docID]++
	}
	for _, id := range docs {
		if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
			Command: "load-investments", SourceDocumentID: id, RowsRead: read[id], RowsWritten: written[id],
		}); err != nil {
			log.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d publications, %d amounts\n", len(docs), len(rows)-1)
}

