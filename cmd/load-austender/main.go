// Command load-austender loads AusTender contract notices from the OCDS API.
//
// It walks contractPublished (or another date field) in weekly windows and
// keeps the latest release of each contract. It then fetches the full history
// of amended contracts to record their original value, and matches AusTender
// agency names to PBS entities. Raw responses are cached under -cache, so a
// re-run only fetches what is missing.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

const publisher = "Department of Finance, AusTender OCDS API"

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	from := flag.String("from", "2022-07-01", "first day to load (YYYY-MM-DD)")
	to := flag.String("to", time.Now().Format("2006-01-02"), "last day to load (YYYY-MM-DD)")
	field := flag.String("field", "contractPublished", "date field: contractPublished or contractLastModified")
	cache := flag.String("cache", ".local/austender", "raw response cache directory")
	refresh := flag.Bool("refresh", false, "re-fetch windows that are already cached")
	history := flag.Bool("history", true, "fetch full history for amended contracts")
	aliases := flag.String("aliases", "data/agency_aliases.csv", "reviewed AusTender agency -> PBS entity names")
	matchOnly := flag.Bool("match-only", false, "only match agency names to entities; fetch nothing")
	flag.Parse()

	start, err := time.Parse("2006-01-02", *from)
	if err != nil {
		log.Fatal(err)
	}
	end, err := time.Parse("2006-01-02", *to)
	if err != nil {
		log.Fatal(err)
	}
	end = end.AddDate(0, 0, 1)

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *matchOnly {
		if err := matchAgencies(ctx, db.New(conn), *aliases); err != nil {
			log.Fatal(err)
		}
		return
	}

	l := &loader{conn: conn, client: austender.NewClient(*cache)}
	recent := time.Now().AddDate(0, 0, -2)
	for w := start; w.Before(end); w = w.AddDate(0, 0, 7) {
		wEnd := w.AddDate(0, 0, 7)
		if wEnd.After(end) {
			wEnd = end
		}
		// Recent windows are always re-fetched so later runs pick up new notices.
		l.client.Refresh = *refresh || wEnd.After(recent)
		n, err := l.window(ctx, *field, w, wEnd)
		if err != nil {
			log.Fatalf("window %s: %v", w.Format("2006-01-02"), err)
		}
		log.Printf("%s to %s: %d contracts", w.Format("2006-01-02"), wEnd.AddDate(0, 0, -1).Format("2006-01-02"), n)
	}
	l.client.Refresh = *refresh
	if *history {
		if err := l.histories(ctx); err != nil {
			log.Fatal(err)
		}
	}
	if err := matchAgencies(ctx, db.New(conn), *aliases); err != nil {
		log.Fatal(err)
	}
}

type loader struct {
	conn   *sql.DB
	client *austender.Client
}

// window loads every page for one date window in a single transaction.
func (l *loader) window(ctx context.Context, field string, from, to time.Time) (int, error) {
	first := austender.ByDatesURL(field, from, to)
	var bodies [][]byte
	var contracts []austender.Contract
	for url := first; url != ""; {
		body, err := l.client.Get(ctx, url)
		if err != nil {
			return 0, err
		}
		page, err := austender.ParsePage(body)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", url, err)
		}
		bodies = append(bodies, body)
		for _, r := range page.Releases {
			cs, err := r.Flatten()
			if err != nil {
				return 0, err
			}
			contracts = append(contracts, cs...)
		}
		url = page.Links.Next
	}

	tx, err := l.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := db.New(tx)
	day := from.Format("2006-01-02")
	title := fmt.Sprintf("AusTender contract notices, %s %s to %s", field, day, to.AddDate(0, 0, -1).Format("2006-01-02"))
	docID, err := source.RegisterData(ctx, q, bytes.Join(bodies, nil), fmt.Sprintf("%d API pages", len(bodies)),
		title, publisher, first, "austender_ocds", austender.FiscalYear(day))
	if err != nil {
		return 0, err
	}
	for _, c := range contracts {
		if err := saveContract(ctx, q, c, docID); err != nil {
			return 0, fmt.Errorf("%s: %w", c.CNID, err)
		}
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-austender", SourceDocumentID: docID, RowsRead: int64(len(contracts)), RowsWritten: int64(len(contracts)),
	}); err != nil {
		return 0, err
	}
	return len(contracts), tx.Commit()
}

func saveContract(ctx context.Context, q *db.Queries, c austender.Contract, docID int64) error {
	agencyID, err := q.UpsertAgency(ctx, db.UpsertAgencyParams{Name: c.Agency, Abn: c.AgencyABN})
	if err != nil {
		return err
	}
	var supplierID sql.NullInt64
	if c.Supplier != "" {
		id, err := q.UpsertSupplier(ctx, db.UpsertSupplierParams{
			Key: austender.SupplierKey(c.Supplier, c.SupplierABN), Abn: c.SupplierABN, Name: c.Supplier,
			Country: c.SupplierCountry, Postcode: c.SupplierPostcode,
		})
		if err != nil {
			return err
		}
		supplierID = sql.NullInt64{Int64: id, Valid: true}
	}
	var original sql.NullInt64
	if len(c.AmendmentIDs) == 0 {
		original = sql.NullInt64{Int64: c.Value, Valid: true}
	}
	published := c.Signed
	if published == "" {
		published = c.ReleaseDate
	}
	if err := q.UpsertContract(ctx, db.UpsertContractParams{
		CnID: c.CNID, Ocid: c.OCID, AgencyID: agencyID, SupplierID: supplierID, Title: c.Title,
		Description: c.Description, Unspsc: c.UNSPSC, Method: c.Method, MethodDetail: c.MethodDetail,
		LimitedReason: c.LimitedReason, Exemption: c.Exemption, StartDate: c.StartDate, EndDate: c.EndDate,
		PublishedDate: published, Value: c.Value, OriginalValue: original, AmendmentCount: int64(len(c.AmendmentIDs)),
		Status: c.Status, ReleaseID: c.ReleaseID, ReleaseDate: c.ReleaseDate, SourceDocumentID: docID,
	}); err != nil {
		return err
	}
	return q.UpsertContractRelease(ctx, db.UpsertContractReleaseParams{
		ReleaseID: c.ReleaseID, CnID: c.CNID, ReleaseDate: c.ReleaseDate, Amendment: boolInt(c.Amendment),
		Value: c.Value, SourceDocumentID: docID,
	})
}

// histories fetches every release of amended contracts and records the value
// of the first, unamended release.
func (l *loader) histories(ctx context.Context) error {
	ids, err := db.New(l.conn).ContractsNeedingHistory(ctx)
	if err != nil {
		return err
	}
	log.Printf("fetching history for %d amended contracts", len(ids))
	for i, id := range ids {
		if err := l.history(ctx, id); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if (i+1)%250 == 0 {
			log.Printf("  history %d/%d", i+1, len(ids))
		}
	}
	return nil
}

func (l *loader) history(ctx context.Context, id string) error {
	url := austender.ByIDURL(id)
	body, err := l.client.Get(ctx, url)
	if err != nil {
		return err
	}
	page, err := austender.ParsePage(body)
	if err != nil {
		return err
	}
	tx, err := l.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := db.New(tx)
	docID, err := source.RegisterData(ctx, q, body, "1 API page", "AusTender contract history, "+id, publisher, url, "austender_ocds_history", "")
	if err != nil {
		return err
	}
	var original sql.NullInt64
	firstDate := ""
	for _, r := range page.Releases {
		cs, err := r.Flatten()
		if err != nil {
			return err
		}
		for _, c := range cs {
			if c.CNID != id {
				continue
			}
			if err := q.UpsertContractRelease(ctx, db.UpsertContractReleaseParams{
				ReleaseID: c.ReleaseID, CnID: id, ReleaseDate: c.ReleaseDate, Amendment: boolInt(c.Amendment),
				Value: c.Value, SourceDocumentID: docID,
			}); err != nil {
				return err
			}
			if !c.Amendment && (firstDate == "" || c.ReleaseDate < firstDate) {
				firstDate, original = c.ReleaseDate, sql.NullInt64{Int64: c.Value, Valid: true}
			}
		}
	}
	if err := q.SetContractHistory(ctx, db.SetContractHistoryParams{OriginalValue: original, CnID: id}); err != nil {
		return err
	}
	return tx.Commit()
}

// matchAgencies links AusTender agency names to PBS entities: first from the
// reviewed alias file, then by exact or normalised name, including names the
// entity was published under in an earlier PBS. An alias with no entity marks
// an agency that is deliberately left unmatched. It logs the names left for
// manual or LLM matching.
func matchAgencies(ctx context.Context, q *db.Queries, aliasFile string) error {
	ents, err := q.ListEntities(ctx)
	if err != nil {
		return err
	}
	pbsNames, err := q.ListEntityAliases(ctx)
	if err != nil {
		return err
	}
	manual, err := source.ReadNames(aliasFile)
	if err != nil {
		return err
	}
	exact := map[string]int64{}
	norm := map[string]int64{}
	for _, a := range pbsNames {
		exact[a.Alias] = a.EntityID
		norm[austender.NameKey(a.Alias)] = a.EntityID
	}
	// Current names win over earlier ones that normalise the same way.
	for _, e := range ents {
		exact[e.Name] = e.ID
		norm[austender.NameKey(e.Name)] = e.ID
	}
	unmatched, err := q.UnmatchedAgencies(ctx)
	if err != nil {
		return err
	}
	var left []string
	excluded := 0
	for _, a := range unmatched {
		if entity, ok := manual[a.Name]; ok && entity == "" {
			if err := q.SetAgencyEntity(ctx, db.SetAgencyEntityParams{MatchMethod: "excluded", ID: a.ID}); err != nil {
				return err
			}
			excluded++
			continue
		}
		id, method := exact[manual[a.Name]], "manual"
		if id == 0 {
			id, method = exact[a.Name], "exact"
		}
		if id == 0 {
			id, method = norm[austender.NameKey(a.Name)], "normalised"
		}
		if id == 0 {
			left = append(left, a.Name)
			continue
		}
		if err := q.SetAgencyEntity(ctx, db.SetAgencyEntityParams{EntityID: sql.NullInt64{Int64: id, Valid: true}, MatchMethod: method, ID: a.ID}); err != nil {
			return err
		}
	}
	log.Printf("agencies: %d newly matched, %d excluded by the alias file, %d unmatched", len(unmatched)-len(left)-excluded, excluded, len(left))
	for _, n := range left {
		log.Printf("  unmatched agency: %s", n)
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
