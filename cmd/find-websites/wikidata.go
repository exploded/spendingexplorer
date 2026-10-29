package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
	"spendingexplorer/internal/website"
)

const (
	sparqlURL    = "https://query.wikidata.org/sparql"
	wikidataFile = "wikidata-websites.json"
)

// wikidataSite is one Wikidata item's official website (P856), keyed by the
// ABN (P3548) or LEI (P1278) it was found by.
type wikidataSite struct {
	Key  string // "abn:<11 digits>" or "lei:<LEI>"
	Item string
	URL  string
}

func sparql(ctx context.Context, query string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sparqlURL, strings.NewReader(url.Values{"query": {query}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("User-Agent", website.UserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("Wikidata: HTTP %d: %s", resp.StatusCode, b)
	}
	var res struct {
		Results struct {
			Bindings []map[string]struct{ Value string }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	out := make([]map[string]string, len(res.Results.Bindings))
	for i, b := range res.Results.Bindings {
		out[i] = map[string]string{}
		for k, v := range b {
			out[i][k] = v.Value
		}
	}
	return out, nil
}

var nonDigit = regexp.MustCompile(`\D`)

// fetchWikidata asks Wikidata for the website of every item with an ABN, and
// of every item with one of the LEIs our suppliers or their parents have.
func fetchWikidata(ctx context.Context, q *db.Queries) ([]wikidataSite, error) {
	byABN, err := sparql(ctx, `SELECT ?item ?abn ?site WHERE { ?item wdt:P3548 ?abn ; wdt:P856 ?site . }`)
	if err != nil {
		return nil, err
	}
	leis, err := q.SupplierLEIs(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, l := range leis {
		want[l.Lei] = true
		if l.ParentLei != "" {
			want[l.ParentLei] = true
		}
	}
	values := make([]string, 0, len(want))
	for l := range want {
		values = append(values, `"`+l+`"`)
	}
	sort.Strings(values)
	var byLEI []map[string]string
	for i := 0; i < len(values); i += 500 {
		rows, err := sparql(ctx, `SELECT ?item ?lei ?site WHERE { VALUES ?lei { `+strings.Join(values[i:min(i+500, len(values))], " ")+
			` } ?item wdt:P1278 ?lei ; wdt:P856 ?site . }`)
		if err != nil {
			return nil, err
		}
		byLEI = append(byLEI, rows...)
	}

	// An item can list several sites; keep one by a fixed rule, so a refresh
	// doesn't swap them at random: an .au site first, then the shortest.
	best := map[string]wikidataSite{}
	add := func(key, item, site string) {
		s := wikidataSite{Key: key, Item: strings.TrimPrefix(item, "http://www.wikidata.org/entity/"), URL: site}
		if old, ok := best[key]; !ok || better(s.URL, old.URL) {
			best[key] = s
		}
	}
	for _, r := range byABN {
		if abn := nonDigit.ReplaceAllString(r["abn"], ""); len(abn) == 11 {
			add("abn:"+abn, r["item"], r["site"])
		}
	}
	for _, r := range byLEI {
		add("lei:"+r["lei"], r["item"], r["site"])
	}
	out := make([]wikidataSite, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	log.Printf("Wikidata: %d sites by ABN, %d by LEI", len(byABN), len(byLEI))
	return out, nil
}

func better(a, b string) bool {
	au := func(s string) bool {
		u, err := url.Parse(s)
		return err == nil && strings.HasSuffix(u.Hostname(), ".au")
	}
	if au(a) != au(b) {
		return au(a)
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// wikidata loads the snapshot: the supplier's own item by ABN, then by LEI,
// then its GLEIF ultimate parent's item. Sites whose domain no longer exists
// are left out.
func wikidata(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	path := filepath.Join(o.raw, wikidataFile)
	if o.refresh {
		sites, err := fetchWikidata(ctx, q)
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(sites, "", " ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	var sites []wikidataSite
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w (run with -fetch)", err)
	}
	if err := json.Unmarshal(b, &sites); err != nil {
		return err
	}
	byKey := map[string]wikidataSite{}
	for _, s := range sites {
		byKey[s.Key] = s
	}

	type pick struct {
		supplierID int64
		site       wikidataSite
		basis      string
		parent     string
		via        string
	}
	picks := map[int64]pick{}
	abns, err := q.WebsiteSupplierABNs(ctx)
	if err != nil {
		return err
	}
	for _, a := range abns {
		if s, ok := byKey["abn:"+a.Abn]; ok {
			picks[a.ID] = pick{a.ID, s, "wikidata", "", "ABN"}
		}
	}
	leis, err := q.SupplierLEIs(ctx)
	if err != nil {
		return err
	}
	for _, l := range leis {
		if _, ok := picks[l.ID]; ok {
			continue
		}
		if s, ok := byKey["lei:"+l.Lei]; ok {
			picks[l.ID] = pick{l.ID, s, "wikidata", "", "LEI"}
		} else if s, ok := byKey["lei:"+l.ParentLei]; ok && l.ParentLei != "" {
			picks[l.ID] = pick{l.ID, s, "wikidata-parent", l.ParentName, "parent's LEI"}
		}
	}

	// Check each distinct site once.
	urls := map[string]bool{}
	for _, p := range picks {
		urls[p.site.URL] = true
	}
	list := make([]string, 0, len(urls))
	for u := range urls {
		list = append(list, u)
	}
	ck := website.NewChecker()
	dead := map[string]string{}
	if err := each(list, o.workers, func(u string) error {
		_, _, err := ck.Fetch(ctx, u)
		return err
	}, func(u string, err error) error {
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			dead[u] = "no such domain"
		}
		return nil
	}); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tq := db.New(tx)
	doc, err := source.Register(ctx, tq, path, "Wikidata: official websites of items with an ABN or LEI", "Wikidata", sparqlURL, "wikidata_websites", "")
	if err != nil {
		return err
	}
	if err := tq.ClearWikidataWebsites(ctx); err != nil {
		return err
	}
	written, parents := 0, 0
	for _, p := range picks {
		detail := fmt.Sprintf("Wikidata %s, by %s", p.site.Item, p.via)
		if why, ok := dead[p.site.URL]; ok {
			if err := tq.PutWebsiteCheck(ctx, db.PutWebsiteCheckParams{
				SupplierID: p.supplierID, Url: p.site.URL, Method: "wikidata", Passed: 0, Detail: detail + ": " + why, CheckedAt: today(),
			}); err != nil {
				return err
			}
			continue
		}
		if err := tq.PutWebsite(ctx, db.PutWebsiteParams{
			SupplierID: p.supplierID, Url: p.site.URL, Basis: p.basis, ParentName: p.parent, Evidence: detail, CheckedAt: today(),
		}); err != nil {
			return err
		}
		written++
		if p.basis == "wikidata-parent" {
			parents++
		}
	}
	notes := fmt.Sprintf("%d supplier websites from Wikidata (%d the parent company's); %d left out because the domain no longer exists",
		written, parents, len(dead))
	if err := tq.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "find-websites wikidata", SourceDocumentID: doc, RowsRead: int64(len(sites)), RowsWritten: int64(written), Notes: notes,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Print(notes)
	return nil
}
