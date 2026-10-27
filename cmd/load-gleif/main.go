// Command load-gleif loads Legal Entity Identifier (LEI) records for supplier
// companies, and the ultimate parent companies they report, from GLEIF's
// golden copy files (CC0):
//
//	https://www.gleif.org/en/lei-data/gleif-golden-copy/download-the-golden-copy
//
// Download the LEI-CDF (lei2) and relationship record (rr) full files, CSV
// format, into -dir first. Australian records give the company's ACN (or
// ABN), which is matched to supplier ABNs: a company's ABN is its ACN with two
// check digits in front. Parents come from active IS_ULTIMATELY_CONSOLIDATED_BY
// relationships, or IS_DIRECTLY_CONSOLIDATED_BY where no ultimate parent is
// reported. Only relationship records GLEIF lists as current (PUBLISHED) are
// used: a lapsed record can describe an ownership that has since ended.
package main

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"time"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

const downloadURL = "https://www.gleif.org/en/lei-data/gleif-golden-copy/download-the-golden-copy"

// entity is the part of an LEI record the site uses.
type entity struct {
	LEI, Name, Country, Status, Registration string
	abn                                      string
}

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	dir := flag.String("dir", ".local/gleif", "folder holding the golden copy zip files")
	flag.Parse()

	leiZip := one(*dir, "*lei2*.csv.zip")
	rrZip := one(*dir, "*rr*.csv.zip")

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

	abns, err := q.SupplierABNs(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// Supplier ABNs by the identifiers an Australian LEI record may carry.
	want := make(map[string]string, 2*len(abns))
	for _, a := range abns {
		if len(a) == 11 {
			want[a] = a
			want[a[2:]] = a
		}
	}
	log.Printf("%d supplier ABNs to look up", len(abns))

	start := time.Now()
	rrDoc, err := source.Register(ctx, q, rrZip, "GLEIF golden copy: relationship records", "Global Legal Entity Identifier Foundation", downloadURL, "gleif_rr", "")
	if err != nil {
		log.Fatal(err)
	}
	ultimate, direct, err := readParents(rrZip)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d ultimate and %d direct parent relationships", len(ultimate), len(direct))
	isParent := map[string]bool{}
	for _, p := range ultimate {
		isParent[p] = true
	}
	for _, p := range direct {
		isParent[p] = true
	}

	leiDoc, err := source.Register(ctx, q, leiZip, "GLEIF golden copy: LEI records", "Global Legal Entity Identifier Foundation", downloadURL, "gleif_lei", "")
	if err != nil {
		log.Fatal(err)
	}
	matched, parents, scanned, err := readEntities(leiZip, want, isParent)
	if err != nil {
		log.Fatal(err)
	}

	if err := q.ClearGLEIFParents(ctx); err != nil {
		log.Fatal(err)
	}
	if err := q.ClearGLEIFEntities(ctx); err != nil {
		log.Fatal(err)
	}
	kept := map[string]bool{}
	for _, e := range matched {
		if err := q.InsertGLEIFEntity(ctx, db.InsertGLEIFEntityParams{
			Lei: e.LEI, LegalName: e.Name, Country: e.Country, Abn: e.abn, Status: e.Status, SourceDocumentID: leiDoc,
		}); err != nil {
			log.Fatal(err)
		}
		kept[e.LEI] = true
	}
	withParent := 0
	for _, e := range matched {
		p := ultimate[e.LEI]
		if p == "" {
			p = direct[e.LEI]
		}
		pe, ok := parents[p]
		if !ok {
			continue
		}
		if !kept[p] {
			if err := q.InsertGLEIFEntity(ctx, db.InsertGLEIFEntityParams{
				Lei: pe.LEI, LegalName: pe.Name, Country: pe.Country, Status: pe.Status, SourceDocumentID: leiDoc,
			}); err != nil {
				log.Fatal(err)
			}
			kept[p] = true
		}
		if err := q.InsertGLEIFParent(ctx, db.InsertGLEIFParentParams{Lei: e.LEI, ParentLei: p, SourceDocumentID: rrDoc}); err != nil {
			log.Fatal(err)
		}
		withParent++
	}
	notes := fmt.Sprintf("%d of %d supplier ABNs have an LEI; %d report a parent", len(matched), len(abns), withParent)
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-gleif", SourceDocumentID: leiDoc, RowsRead: scanned, RowsWritten: int64(len(kept)), Notes: notes,
	}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	log.Printf("scanned %d LEI records in %s; %s", scanned, time.Since(start).Round(time.Second), notes)
}

// one returns the single file in dir matching pattern.
func one(dir, pattern string) string {
	m, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil || len(m) != 1 {
		log.Fatalf("want one %s file in %s, found %d (download the CSV full files from %s)", pattern, dir, len(m), downloadURL)
	}
	return m[0]
}

// openCSV opens the single CSV in a zip and returns a reader positioned after
// the header, with a function giving each named column's index.
func openCSV(path string) (*csv.Reader, func(string) int, io.Closer, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(zr.File) != 1 {
		zr.Close()
		return nil, nil, nil, fmt.Errorf("%s: want one file, found %d", path, len(zr.File))
	}
	f, err := zr.File[0].Open()
	if err != nil {
		zr.Close()
		return nil, nil, nil, err
	}
	r := csv.NewReader(f)
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		zr.Close()
		return nil, nil, nil, err
	}
	cols := map[string]int{}
	for i, h := range header {
		cols[h] = i
	}
	col := func(name string) int {
		i, ok := cols[name]
		if !ok {
			log.Fatalf("%s: no column %q", path, name)
		}
		return i
	}
	return r, col, zr, nil
}

// readParents returns each entity's active ultimate and direct parent LEIs.
func readParents(path string) (ultimate, direct map[string]string, err error) {
	r, col, c, err := openCSV(path)
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	child, parent, parentType := col("Relationship.StartNode.NodeID"), col("Relationship.EndNode.NodeID"), col("Relationship.EndNode.NodeIDType")
	kind, status, reg := col("Relationship.RelationshipType"), col("Relationship.RelationshipStatus"), col("Registration.RegistrationStatus")
	ultimate, direct = map[string]string{}, map[string]string{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return ultimate, direct, nil
		} else if err != nil {
			return nil, nil, err
		}
		if rec[status] != "ACTIVE" || rec[parentType] != "LEI" || rec[reg] != "PUBLISHED" {
			continue
		}
		switch rec[kind] {
		case "IS_ULTIMATELY_CONSOLIDATED_BY":
			ultimate[rec[child]] = rec[parent]
		case "IS_DIRECTLY_CONSOLIDATED_BY":
			direct[rec[child]] = rec[parent]
		}
	}
}

// readEntities scans the LEI records once. It returns Australian records whose
// ACN or ABN belongs to a supplier (one per ABN, preferring an active, issued
// record), and every record that is someone's parent.
func readEntities(path string, want map[string]string, isParent map[string]bool) (map[string]entity, map[string]entity, int64, error) {
	r, col, c, err := openCSV(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer c.Close()
	lei, name, country, juris := col("LEI"), col("Entity.LegalName"), col("Entity.LegalAddress.Country"), col("Entity.LegalJurisdiction")
	regID, status, reg := col("Entity.RegistrationAuthority.RegistrationAuthorityEntityID"), col("Entity.EntityStatus"), col("Registration.RegistrationStatus")
	matched, parents := map[string]entity{}, map[string]entity{}
	var n int64
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, nil, n, err
		}
		n++
		if n%500000 == 0 {
			log.Printf("  %d LEI records read", n)
		}
		if rec[reg] == "RETIRED" || rec[reg] == "ANNULLED" || rec[reg] == "DUPLICATE" || rec[reg] == "MERGED" {
			continue
		}
		e := entity{LEI: rec[lei], Name: rec[name], Country: rec[country], Status: rec[status], Registration: rec[reg]}
		if isParent[e.LEI] {
			parents[e.LEI] = e
		}
		if !strings.HasPrefix(rec[juris], "AU") {
			continue
		}
		abn := want[digits(rec[regID])]
		if abn == "" {
			continue
		}
		e.abn = abn
		if old, ok := matched[abn]; !ok || rank(e) > rank(old) {
			matched[abn] = e
		}
	}
	return matched, parents, n, nil
}

// rank prefers active entities with current registrations.
func rank(e entity) int {
	r := 0
	if e.Status == "ACTIVE" {
		r += 2
	}
	if e.Registration == "ISSUED" {
		r++
	}
	return r
}

func digits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}
