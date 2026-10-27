// Command load-abr loads Australian Business Register details for the ABNs of
// AusTender suppliers from the ABN Bulk Extract on data.gov.au:
//
//	https://data.gov.au/data/dataset/abn-bulk-extract
//
// Download public_split_1_10.zip and public_split_11_20.zip into -dir first.
// The extract holds about 20 million records, one per line; the loader scans
// each line for its ABN and parses only those that appear in AusTender. It
// keeps entity type and status, not names. It also counts the given names of
// registered individuals, to recognise people's names among suppliers that
// have no ABN.
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/source"
)

const datasetURL = "https://data.gov.au/data/dataset/abn-bulk-extract"

// minGivenName is how many registrations a given name needs to be kept, which
// leaves out rare names and one-off entry errors.
const minGivenName = 200

// record is the part of an <ABR> element the site uses.
type record struct {
	Updated string `xml:"recordLastUpdatedDate,attr"`
	ABN     struct {
		Status string `xml:"status,attr"`
		From   string `xml:"ABNStatusFromDate,attr"`
		Value  string `xml:",chardata"`
	} `xml:"ABN"`
	EntityType struct {
		Code string `xml:"EntityTypeInd"`
		Text string `xml:"EntityTypeText"`
	} `xml:"EntityType"`
}

type match struct {
	rec   record
	docID int64
}

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	dir := flag.String("dir", ".local/abr", "folder holding the bulk extract zip files")
	flag.Parse()

	zips, err := filepath.Glob(filepath.Join(*dir, "public_split_*.zip"))
	if err != nil || len(zips) == 0 {
		log.Fatalf("no public_split_*.zip files in %s (download them from %s)", *dir, datasetURL)
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

	abns, err := q.SupplierABNs(ctx)
	if err != nil {
		log.Fatal(err)
	}
	want := make(map[string]bool, len(abns))
	for _, a := range abns {
		want[a] = true
	}
	log.Printf("%d supplier ABNs to look up", len(want))

	var (
		mu      sync.Mutex
		found   []match
		scanned int64
		given   = map[string]int64{}
	)
	type job struct {
		f     *zip.File
		docID int64
	}
	jobs := make(chan job)
	errs := make(chan error, 64)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				n, ms, names, err := scanFile(j.f, want)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", j.f.Name, err)
					continue
				}
				mu.Lock()
				scanned += n
				for _, r := range ms {
					found = append(found, match{r, j.docID})
				}
				for name, c := range names {
					given[name] += c
				}
				mu.Unlock()
				log.Printf("  %s: %d records, %d matched", j.f.Name, n, len(ms))
			}
		}()
	}

	start := time.Now()
	var readers []*zip.ReadCloser
	for _, path := range zips {
		docID, err := source.Register(ctx, q, path, "ABN Bulk Extract ("+filepath.Base(path)+")",
			"Australian Business Register", datasetURL, "abr_bulk_extract", "")
		if err != nil {
			log.Fatal(err)
		}
		zr, err := zip.OpenReader(path)
		if err != nil {
			log.Fatal(err)
		}
		readers = append(readers, zr)
		for _, f := range zr.File {
			jobs <- job{f, docID}
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for _, zr := range readers {
		zr.Close()
	}
	for err := range errs {
		log.Fatal(err)
	}

	for _, m := range found {
		r := m.rec
		if err := q.UpsertABREntity(ctx, db.UpsertABREntityParams{
			Abn: r.ABN.Value, EntityType: r.EntityType.Code, EntityTypeText: r.EntityType.Text,
			Status: r.ABN.Status, StatusFrom: isoDate(r.ABN.From), RecordUpdated: isoDate(r.Updated),
			SourceDocumentID: m.docID,
		}); err != nil {
			log.Fatal(err)
		}
	}
	if err := q.ClearGivenNames(ctx); err != nil {
		log.Fatal(err)
	}
	kept := 0
	for name, c := range given {
		if c < minGivenName {
			continue
		}
		if err := q.InsertGivenName(ctx, db.InsertGivenNameParams{Name: name, Registrations: c}); err != nil {
			log.Fatal(err)
		}
		kept++
	}
	if err := q.InsertLoadRun(ctx, db.InsertLoadRunParams{
		Command: "load-abr", SourceDocumentID: found[0].docID, RowsRead: scanned, RowsWritten: int64(len(found)),
		Notes: fmt.Sprintf("%d of %d supplier ABNs found; %d given names kept", len(found), len(want), kept),
	}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	log.Printf("scanned %d records in %s; %d of %d supplier ABNs found; %d given names kept",
		scanned, time.Since(start).Round(time.Second), len(found), len(want), kept)
}

// scanFile reads one XML file of the extract and returns the records whose
// ABN is wanted, and a count of individuals' given names. Each <ABR> element
// sits on one line.
func scanFile(f *zip.File, want map[string]bool) (int64, []record, map[string]int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, nil, nil, err
	}
	defer rc.Close()
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var n int64
	var out []record
	names := map[string]int64{}
	for sc.Scan() {
		line := sc.Bytes()
		i := bytes.Index(line, []byte("<ABR "))
		if i < 0 {
			continue
		}
		n++
		if bytes.Contains(line, []byte("<EntityTypeInd>IND<")) {
			countGivenNames(line, names)
		}
		abn := abnOf(line[i:])
		if abn == "" || !want[abn] {
			continue
		}
		end := bytes.Index(line[i:], []byte("</ABR>"))
		if end < 0 {
			return n, out, names, fmt.Errorf("record for ABN %s not closed on its line", abn)
		}
		var r record
		if err := xml.Unmarshal(line[i:i+end+len("</ABR>")], &r); err != nil {
			return n, out, names, fmt.Errorf("ABN %s: %w", abn, err)
		}
		out = append(out, r)
	}
	return n, out, names, sc.Err()
}

// countGivenNames adds each <GivenName> in an individual's record to names.
func countGivenNames(line []byte, names map[string]int64) {
	open, close := []byte("<GivenName>"), []byte("</GivenName>")
	for {
		i := bytes.Index(line, open)
		if i < 0 {
			return
		}
		line = line[i+len(open):]
		j := bytes.Index(line, close)
		if j < 0 {
			return
		}
		if name := strings.ToUpper(strings.TrimSpace(string(line[:j]))); len(name) > 1 {
			names[name]++
		}
		line = line[j:]
	}
}

// abnOf returns the text of the first <ABN ...>...</ABN> element in b.
func abnOf(b []byte) string {
	i := bytes.Index(b, []byte("<ABN "))
	if i < 0 {
		return ""
	}
	j := bytes.IndexByte(b[i:], '>')
	if j < 0 {
		return ""
	}
	rest := b[i+j+1:]
	k := bytes.IndexByte(rest, '<')
	if k < 0 {
		return ""
	}
	return string(rest[:k])
}

// isoDate turns YYYYMMDD into YYYY-MM-DD.
func isoDate(s string) string {
	if len(s) != 8 {
		return s
	}
	return s[:4] + "-" + s[4:6] + "-" + s[6:]
}
