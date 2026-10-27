// Command load-projects loads data/projects.csv and data/project_estimates.csv,
// curated by hand, into project and project_estimate. Every estimate must cite
// a source URL and quote the source's words containing the figure.
package main

import (
	"context"
	"database/sql"
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
	"spendingexplorer/internal/source"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}(-\d{2})?$`)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	projectsPath := flag.String("projects", "data/projects.csv", "projects CSV")
	estimatesPath := flag.String("estimates", "data/project_estimates.csv", "project estimates CSV")
	flag.Parse()

	projects, err := readCSV(*projectsPath)
	if err != nil {
		log.Fatal(err)
	}
	estimates, err := readCSV(*estimatesPath)
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

	docID, err := source.Register(ctx, q, *estimatesPath, "Project cost estimates, compiled from published sources",
		"Federal Spending Explorer (each estimate cites its source)", "https://github.com/exploded/spendingexplorer/blob/main/data/project_estimates.csv",
		"project_estimates", "")
	if err != nil {
		log.Fatal(err)
	}
	if err := q.ClearProjectLinks(ctx); err != nil {
		log.Fatal(err)
	}
	if err := q.ClearProjectEstimates(ctx); err != nil {
		log.Fatal(err)
	}
	if err := q.ClearProjects(ctx); err != nil {
		log.Fatal(err)
	}

	// key, name, owner, summary, item_keys, funding_note, entities, supplier_abns, links_note
	known := map[string]bool{}
	links := 0
	for i, p := range projects {
		if len(p) < 8 {
			log.Fatalf("project %q: want entities and supplier_abns columns", p[0])
		}
		if err := q.InsertProject(ctx, db.InsertProjectParams{
			Key: p[0], Name: p[1], Owner: p[2], Summary: p[3], ItemKeys: p[4], FundingNote: p[5], Sort: int64(i),
		}); err != nil {
			log.Fatalf("project %q: %v", p[0], err)
		}
		known[p[0]] = true
		for _, name := range split(p[6]) {
			id, err := q.EntityIDByName(ctx, name)
			if err != nil {
				log.Fatalf("project %q: entity %q: %v", p[0], name, err)
			}
			if err := q.InsertProjectLink(ctx, db.InsertProjectLinkParams{ProjectKey: p[0], EntityID: sql.NullInt64{Int64: id, Valid: true}}); err != nil {
				log.Fatal(err)
			}
			links++
		}
		for _, abn := range split(p[7]) {
			ids, err := q.SupplierIDsByABN(ctx, abn)
			if err != nil {
				log.Fatal(err)
			}
			if len(ids) == 0 {
				log.Printf("project %q: no supplier has ABN %s, so no supplier page links to it", p[0], abn)
			}
			for _, id := range ids {
				if err := q.InsertProjectLink(ctx, db.InsertProjectLinkParams{ProjectKey: p[0], SupplierID: sql.NullInt64{Int64: id, Valid: true}}); err != nil {
					log.Fatal(err)
				}
				links++
			}
		}
	}

	// project, published, amount_m, amount_high_m, scope, scope_changed, completion,
	// source_title, source_publisher, source_url, quote, secondary, video_url, video_title
	for n, e := range estimates {
		line := n + 2
		switch {
		case !known[e[0]]:
			log.Fatalf("line %d: unknown project %q", line, e[0])
		case !dateRe.MatchString(e[1]):
			log.Fatalf("line %d: published %q is not YYYY-MM or YYYY-MM-DD", line, e[1])
		case e[4] == "" || e[7] == "" || e[8] == "" || e[10] == "":
			log.Fatalf("line %d: scope, source title, publisher and quote are required", line)
		case !strings.HasPrefix(e[9], "https://"), e[12] != "" && !strings.HasPrefix(e[12], "https://"):
			log.Fatalf("line %d: source and video URLs must be https", line)
		}
		amount, err := dollars(e[2])
		if err != nil || amount <= 0 {
			log.Fatalf("line %d: amount_m %q", line, e[2])
		}
		var high sql.NullInt64
		if e[3] != "" {
			v, err := dollars(e[3])
			if err != nil || v <= amount {
				log.Fatalf("line %d: amount_high_m %q must exceed amount_m", line, e[3])
			}
			high = sql.NullInt64{Int64: v, Valid: true}
		}
		if err := q.InsertProjectEstimate(ctx, db.InsertProjectEstimateParams{
			ProjectKey: e[0], Published: e[1], Amount: amount, AmountHigh: high, Scope: e[4],
			ScopeChanged: flag01(e[5]), Completion: e[6], SourceTitle: e[7], SourcePublisher: e[8],
			SourceUrl: e[9], Quote: e[10], Secondary: flag01(e[11]), VideoUrl: e[12], VideoTitle: e[13],
			SourceDocumentID: docID,
		}); err != nil {
			log.Fatalf("line %d: %v", line, err)
		}
	}

	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-projects", SourceDocumentID: docID, RowsRead: int64(len(estimates)), RowsWritten: int64(len(estimates)),
		Notes: fmt.Sprintf("%d projects, %d links", len(projects), links),
	}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d projects, %d estimates\n", len(projects), len(estimates))
}

// dollars converts an amount in $m, such as "4700" or "5.3", to whole dollars.
func dollars(m string) (int64, error) {
	v, err := strconv.ParseFloat(m, 64)
	return int64(math.Round(v * 1e6)), err
}

// split returns the non-empty values of a |-separated cell.
func split(s string) []string {
	var out []string
	for _, v := range strings.Split(s, "|") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func flag01(s string) int64 {
	if s == "1" {
		return 1
	}
	return 0
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s: empty", path)
	}
	return rows[1:], nil
}
