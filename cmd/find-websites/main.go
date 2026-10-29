// Command find-websites finds suppliers' own websites, cheapest source first:
//
//	find-websites wikidata [-fetch]  # Wikidata by ABN, LEI or GLEIF parent (free)
//	find-websites guess              # domains built from the name, checked here (free)
//	find-websites recheck            # check stored guesses again after the checks change
//	find-websites submit [-n]        # Claude web search for the rest of the top suppliers
//	find-websites collect [-wait]    # store and check the answers of finished batches
//	find-websites test -id 6058      # one search, answered now, nothing stored
//	find-websites report             # coverage so far
//
// guess, collect and test fetch each site from this PC and link it only when
// the page passes the checks in internal/website. Nothing is queued for
// review: a site that fails is recorded in website_check and left out. The
// API key is read as in cmd/llm-classify: never set ANTHROPIC_API_KEY on a
// development PC.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/website"
)

type options struct {
	db, keyFile, raw string
	top, workers     int
	dryRun, refresh  bool
	wait             bool
	id               int64
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: find-websites wikidata|guess|recheck|submit|collect|test|report [flags]")
	}
	cmd := os.Args[1]
	var o options
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.StringVar(&o.db, "db", "spending.db", "SQLite database path")
	fs.StringVar(&o.keyFile, "key-file", ".local/anthropic.key", "file holding the API key; used when ANTHROPIC_API_KEY is not set")
	fs.StringVar(&o.raw, "raw", "data/raw", "folder holding the Wikidata snapshot")
	fs.IntVar(&o.top, "top", 2000, "guess, submit: how many suppliers, largest by contract value")
	fs.IntVar(&o.workers, "workers", 8, "sites fetched at once")
	fs.BoolVar(&o.dryRun, "n", false, "submit: report what would be sent, and its cost, without sending")
	fs.BoolVar(&o.refresh, "fetch", false, "wikidata: download a fresh snapshot first")
	fs.BoolVar(&o.wait, "wait", false, "collect: wait for open batches to finish")
	fs.Int64Var(&o.id, "id", 0, "test: the supplier to search for")
	fs.Parse(os.Args[2:])

	conn, err := db.Open(o.db)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()

	switch cmd {
	case "wikidata":
		err = wikidata(ctx, conn, o)
	case "guess":
		err = guess(ctx, conn, o)
	case "recheck":
		err = recheck(ctx, conn, o)
	case "submit":
		err = submit(ctx, conn, o)
	case "collect":
		err = collect(ctx, conn, o)
	case "test":
		err = test(ctx, conn, o)
	case "report":
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := report(ctx, db.New(conn)); err != nil {
		log.Fatal(err)
	}
}

func today() string { return time.Now().Format(time.DateOnly) }

// recheck applies the current checks again: guessed sites that now fail are
// dropped, so the supplier goes on to the search step, and search answers that
// failed are linked if they now pass. No search is repeated.
func recheck(ctx context.Context, conn *sql.DB, o options) error {
	if err := recheckGuesses(ctx, conn, o); err != nil {
		return err
	}
	return recheckSearches(ctx, conn, o)
}

func recheckSearches(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	rows, err := q.SearchAnswers(ctx)
	if err != nil {
		return err
	}
	ck := website.NewChecker()
	linked := 0
	err = each(rows, o.workers, func(a db.SearchAnswersRow) website.Result {
		s := website.Supplier{ID: a.ID, Name: a.Name, ABN: a.Abn, Postcode: a.Postcode, Country: a.Country}
		return ck.Visit(ctx, s, a.Url, website.Plain)
	}, func(a db.SearchAnswersRow, r website.Result) error {
		if err := q.PutWebsiteCheck(ctx, db.PutWebsiteCheckParams{
			SupplierID: a.ID, Url: a.Url, Method: "search", Passed: boolInt(r.Passed), Detail: r.Evidence + r.Detail, CheckedAt: today(),
		}); err != nil {
			return err
		}
		if r.Passed {
			linked++
			return q.PutWebsite(ctx, db.PutWebsiteParams{
				SupplierID: a.ID, Url: website.Home(r.URL), Basis: "search", Evidence: r.Evidence, CheckedAt: today(),
			})
		}
		// A linked site that is down or blocks us today keeps its link.
		if r.Blocked || !r.Reached {
			return nil
		}
		log.Printf("rejected %s: %s (%s)", a.Name, a.Url, r.Detail)
		return q.DeleteWebsite(ctx, a.ID)
	})
	log.Printf("recheck: %d of %d search answers pass", linked, len(rows))
	return err
}

func recheckGuesses(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	rows, err := q.GuessedWebsites(ctx)
	if err != nil {
		return err
	}
	ck := website.NewChecker()
	return each(rows, o.workers, func(g db.GuessedWebsitesRow) website.Result {
		s := website.Supplier{ID: g.ID, Name: g.Name, ABN: g.Abn, Postcode: g.Postcode, Country: g.Country}
		return ck.Visit(ctx, s, g.Url, website.Strict)
	}, func(g db.GuessedWebsitesRow, r website.Result) error {
		if err := q.PutWebsiteCheck(ctx, db.PutWebsiteCheckParams{
			SupplierID: g.ID, Url: g.Url, Method: "guess", Passed: boolInt(r.Passed), Detail: r.Evidence + r.Detail, CheckedAt: today(),
		}); err != nil {
			return err
		}
		if r.Passed {
			return q.PutWebsite(ctx, db.PutWebsiteParams{
				SupplierID: g.ID, Url: website.Home(r.URL), Basis: "guess", Evidence: r.Evidence, CheckedAt: today(),
			})
		}
		// A site that is down or blocks us today keeps its link.
		if r.Blocked || !r.Reached {
			return nil
		}
		log.Printf("dropped %s: %s (%s)", g.Name, g.Url, r.Detail)
		return q.DeleteWebsite(ctx, g.ID)
	})
}

// candidates returns the top suppliers by contract value, leaving out FMS.
func candidates(ctx context.Context, q *db.Queries, top int) ([]db.WebsiteCandidatesRow, error) {
	rows, err := q.WebsiteCandidates(ctx, int64(top))
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if !austender.IsFMS(r.Name) {
			out = append(out, r)
		}
	}
	return out, nil
}

func supplierOf(r db.WebsiteCandidatesRow) website.Supplier {
	return website.Supplier{ID: r.ID, Name: r.Name, ABN: r.Abn, Postcode: r.Postcode, Country: r.Country}
}

// each runs f over items with n workers and hands each result to done, one at
// a time, in the calling goroutine.
func each[T, R any](items []T, n int, f func(T) R, done func(T, R) error) error {
	type pair struct {
		in  T
		out R
	}
	in := make(chan T)
	out := make(chan pair)
	var wg sync.WaitGroup
	for range max(n, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range in {
				out <- pair{t, f(t)}
			}
		}()
	}
	go func() {
		for _, t := range items {
			in <- t
		}
		close(in)
		wg.Wait()
		close(out)
	}()
	var err error
	for p := range out {
		if err == nil {
			err = done(p.in, p.out)
		}
	}
	return err
}

// guess tries domains built from each supplier's name, under the strict bar.
func guess(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	rows, err := candidates(ctx, q, o.top)
	if err != nil {
		return err
	}
	tried, err := q.GuessedSuppliers(ctx)
	if err != nil {
		return err
	}
	done := map[int64]bool{}
	for _, id := range tried {
		done[id] = true
	}
	var todo []website.Supplier
	for _, r := range rows {
		if r.HasSite == 0 && !done[r.ID] {
			todo = append(todo, supplierOf(r))
		}
	}
	log.Printf("guess: %d of the top %d suppliers have no site and haven't been tried", len(todo), len(rows))
	ck := website.NewChecker()
	found, n := 0, 0
	return each(todo, o.workers, func(s website.Supplier) []website.Result {
		var tries []website.Result
		for _, d := range website.Candidates(s) {
			for _, host := range []string{d, "www." + d} {
				r := ck.Visit(ctx, s, "https://"+host+"/", website.Strict)
				tries = append(tries, r)
				if r.Passed {
					return tries
				}
				if r.Reached {
					break // the domain answered; www is the same site
				}
			}
		}
		return tries
	}, func(s website.Supplier, tries []website.Result) error {
		n++
		for _, r := range tries {
			if r.Detail == "no such domain" {
				continue
			}
			if err := q.PutWebsiteCheck(ctx, db.PutWebsiteCheckParams{
				SupplierID: s.ID, Url: r.URL, Method: "guess", Passed: boolInt(r.Passed), Detail: r.Evidence + r.Detail, CheckedAt: today(),
			}); err != nil {
				return err
			}
			if r.Passed {
				found++
				log.Printf("%s: %s (%s)", s.Name, website.Home(r.URL), r.Evidence)
				if err := q.AddWebsite(ctx, db.AddWebsiteParams{
					SupplierID: s.ID, Url: website.Home(r.URL), Basis: "guess", Evidence: r.Evidence, CheckedAt: today(),
				}); err != nil {
					return err
				}
			}
		}
		// Record the attempt even when no domain existed, so a rerun skips it.
		if len(tries) > 0 && !tries[len(tries)-1].Passed {
			last := tries[len(tries)-1]
			if err := q.PutWebsiteCheck(ctx, db.PutWebsiteCheckParams{
				SupplierID: s.ID, Url: last.URL, Method: "guess", Passed: 0, Detail: last.Detail, CheckedAt: today(),
			}); err != nil {
				return err
			}
		}
		if n%100 == 0 {
			log.Printf("guess: %d of %d tried, %d found", n, len(todo), found)
		}
		return nil
	})
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func report(ctx context.Context, q *db.Queries) error {
	rows, err := q.WebsiteCoverage(ctx)
	if err != nil {
		return err
	}
	var parts []string
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s %d suppliers ($%.1fbn)", r.Basis, r.Suppliers, float64(r.Value)/1e9))
	}
	if len(parts) == 0 {
		parts = []string{"none yet"}
	}
	log.Printf("websites: %s", strings.Join(parts, "; "))
	return nil
}
