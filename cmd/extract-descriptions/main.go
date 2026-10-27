// Command extract-descriptions builds data/program_descriptions_<year>.csv:
// each program's objective, quoted from its Portfolio Budget Statements PDF,
// with the document URL and page. The CSV is committed and reviewed, then
// loaded by load-descriptions, so the server never needs pdftotext.
//
//	extract-descriptions -crawl   # write data/pbs_documents_<year>.csv from budget.gov.au
//	extract-descriptions          # download, extract and match
//
// Requires pdftotext (poppler) on PATH. Documents are cached in .local/pbsdocs.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"spendingexplorer/internal/austender"
	"spendingexplorer/internal/db"
	"spendingexplorer/internal/pbs"
	"spendingexplorer/internal/pbsdoc"
)

const indexURL = "https://budget.gov.au/content/pbs/index.htm"

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path (read for program names)")
	year := flag.String("budget", "2026-27", "budget year")
	crawl := flag.Bool("crawl", false, "rebuild the document list from budget.gov.au")
	cache := flag.String("cache", ".local/pbsdocs", "document cache directory")
	flag.Parse()

	docsCSV := fmt.Sprintf("data/pbs_documents_%s.csv", *year)
	outCSV := fmt.Sprintf("data/program_descriptions_%s.csv", *year)
	ctx := context.Background()
	if *crawl {
		if err := crawlDocuments(ctx, docsCSV, *year); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := extract(ctx, *dbPath, *year, docsCSV, outCSV, *cache); err != nil {
		log.Fatal(err)
	}
}

var (
	client  = &http.Client{Timeout: 120 * time.Second}
	hrefRe  = regexp.MustCompile(`(?i)href="([^"]+)"`)
	skipped = regexp.MustCompile(`budget\.gov\.au|twitter|facebook|linkedin|youtube|instagram|jsdelivr|fonts\.|contact-us`)
)

func get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Some department sites refuse requests without a browser user agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// isPDF accepts direct PDF links and Drupal-style /download/.../pdf links.
func isPDF(u *url.URL) bool {
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, ".pdf") || strings.HasSuffix(p, "/pdf")
}

// pdfLinks returns the PDFs a page links to, and same-site pages that look
// like PBS resource pages (one more level to search).
func pdfLinks(page string, body []byte) (pdfs, subpages []string) {
	base, _ := url.Parse(page)
	for _, l := range hrefRe.FindAllStringSubmatch(string(body), -1) {
		ref, err := url.Parse(strings.ReplaceAll(l[1], "&amp;", "&"))
		if err != nil {
			continue
		}
		u := base.ResolveReference(ref)
		switch {
		case isPDF(u):
			pdfs = append(pdfs, u.String())
		case u.Host == base.Host && u.Path != base.Path && strings.Contains(strings.ToLower(u.Path), "portfolio-budget-statements"):
			subpages = append(subpages, u.String())
		}
	}
	return pdfs, subpages
}

// crawlDocuments lists every PDF linked from each portfolio's PBS page, and
// from PBS resource pages one level below it. Pages that block automated
// requests need rows added by hand.
func crawlDocuments(ctx context.Context, path, year string) error {
	idx, err := get(ctx, indexURL)
	if err != nil {
		return err
	}
	var rows [][]string
	seen := map[string]bool{}
	for _, m := range hrefRe.FindAllStringSubmatch(string(idx), -1) {
		page := m[1]
		if !strings.HasPrefix(page, "http") || skipped.MatchString(page) || seen[page] {
			continue
		}
		seen[page] = true
		body, err := get(ctx, page)
		if err != nil {
			log.Printf("skip %s: %v", page, err)
			continue
		}
		n := 0
		pdfs, subpages := pdfLinks(page, body)
		for _, sp := range subpages {
			if seen[sp] {
				continue
			}
			seen[sp] = true
			if sb, err := get(ctx, sp); err == nil {
				more, _ := pdfLinks(sp, sb)
				pdfs = append(pdfs, more...)
			}
		}
		for _, doc := range pdfs {
			if seen[doc] || !forBudget(doc, year) {
				continue
			}
			seen[doc] = true
			rows = append(rows, []string{page, doc})
			n++
		}
		log.Printf("%3d PDFs  %s", n, page)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0]+rows[i][1] < rows[j][0]+rows[j][1] })
	return writeCSV(path, []string{"portfolio_page", "document_url"}, rows)
}

// program is a candidate match from the database.
type program struct{ name, entity, code string }

var (
	footnotes = regexp.MustCompile(`\s*[(\[][a-z][)\]]`)
	codeRe    = regexp.MustCompile(`(?i)^\s*program\s+(\d+(?:\.\d+)*)`)
)

// matchKey normalises a program heading for matching: footnote markers such
// as "(b)" are dropped before NameKey.
func matchKey(name string) string {
	return austender.NameKey(footnotes.ReplaceAllString(name, ""))
}

// programIndex finds the database program for a document heading.
type programIndex struct {
	byKey    map[string][]program
	keys     []string
	byEntity map[string][]program
}

// lookup tries an exact key, then a unique prefix match for headings cut
// short by line wrapping (or database names that carry extra words).
func (ix programIndex) lookup(heading string) []program {
	k := matchKey(heading)
	if c := ix.byKey[k]; len(c) > 0 {
		return c
	}
	if len(k) < 25 {
		return nil
	}
	var hits []program
	for _, dk := range ix.keys {
		if len(dk) >= 25 && (strings.HasPrefix(dk, k) || strings.HasPrefix(k, dk)) {
			hits = append(hits, ix.byKey[dk]...)
		}
	}
	return hits
}

// byCode finds an entity's program by its number, for headings such as
// "Program 2.2" followed by an "Objective" label.
func (ix programIndex) byCode(entity, code string) []program {
	var hits []program
	for _, p := range ix.byEntity[entity] {
		if p.code == code {
			hits = append(hits, p)
		}
	}
	return hits
}

func extract(ctx context.Context, dbPath, year, docsCSV, outCSV, cache string) error {
	conn, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT p.name, e.name FROM program p JOIN entity e ON e.id = p.entity_id WHERE p.budget_year = ?`, year)
	if err != nil {
		return err
	}
	ix := programIndex{byKey: map[string][]program{}, byEntity: map[string][]program{}}
	for rows.Next() {
		var p program
		if err := rows.Scan(&p.name, &p.entity); err != nil {
			return err
		}
		if m := codeRe.FindStringSubmatch(p.name); m != nil {
			p.code = m[1]
		}
		k := matchKey(p.name)
		if len(ix.byKey[k]) == 0 {
			ix.keys = append(ix.keys, k)
		}
		ix.byKey[k] = append(ix.byKey[k], p)
		ix.byEntity[p.entity] = append(ix.byEntity[p.entity], p)
	}
	rows.Close()

	docs, err := readCSV(docsCSV)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return fmt.Errorf("pdftotext not found on PATH (install poppler)")
	}

	type found struct {
		o   pbsdoc.Objective
		p   program
		url string
	}
	matched := map[string]found{} // by program name + entity
	var unmatched, ambiguous, unverified, otherDocs int
	var misses []string
	for _, d := range docs {
		docURL := d[1]
		if !forBudget(docURL, year) {
			otherDocs++
			continue
		}
		text, err := pdfText(ctx, docURL, cache)
		if err != nil {
			log.Printf("skip %s: %v", docURL, err)
			continue
		}
		if !mentionsYear(text, year) {
			otherDocs++
			continue
		}
		objectives := pbsdoc.Extract(text)
		// First pass by heading; the entity matched most often becomes the
		// document's entity for headings that carry only a number.
		resolved := make([][]program, len(objectives))
		votes := map[string]int{}
		docEntity := ""
		for i, o := range objectives {
			resolved[i] = ix.lookup(o.Heading)
			if len(resolved[i]) == 1 {
				e := resolved[i][0].entity
				votes[e]++
				if votes[e] > votes[docEntity] {
					docEntity = e
				}
			}
		}
		for i, o := range objectives {
			cands := resolved[i]
			if len(cands) == 0 && o.CodeOnly && docEntity != "" {
				cands = ix.byCode(docEntity, o.Code)
			}
			switch len(cands) {
			case 0:
				unmatched++
				misses = append(misses, o.Heading+"\t"+docURL)
				continue
			case 1:
			default:
				ambiguous++
				continue
			}
			if !verify(text, o, cands[0].code) {
				unverified++
				misses = append(misses, "UNVERIFIED "+o.Heading+"\t"+docURL)
				continue
			}
			key := cands[0].entity + "\x00" + cands[0].name
			if prev, ok := matched[key]; ok && len(prev.o.Text) >= len(o.Text) {
				continue
			}
			matched[key] = found{o, cands[0], docURL}
		}
	}

	var out [][]string
	for _, f := range matched {
		out = append(out, []string{year, f.p.entity, f.p.name, f.o.Text, f.url, fmt.Sprint(f.o.Page)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][1]+out[i][2] < out[j][1]+out[j][2] })
	if err := writeCSV(outCSV, []string{"budget_year", "entity", "program", "objective", "source_url", "page"}, out); err != nil {
		return err
	}
	// Headings that matched no program, for reviewing the matching rules.
	os.WriteFile(filepath.Join(cache, "unmatched.txt"), []byte(strings.Join(misses, "\n")), 0o644)
	log.Printf("%d programs described; %d headings unmatched, %d ambiguous, %d failed verification; %d documents from other publications skipped; wrote %s",
		len(out), unmatched, ambiguous, unverified, otherDocs, outCSV)
	return nil
}

var (
	yearToken = regexp.MustCompile(`20(\d\d)[-–_ ]?(?:20)?(\d\d)`)
	otherPub  = regexp.MustCompile(`(?i)paes|additional[-_ ]estimates|myefo`)
)

// forBudget rejects documents for other years or other publications that
// portfolio pages also link, such as Additional Estimates.
func forBudget(docURL, year string) bool {
	u, _ := url.PathUnescape(docURL)
	if otherPub.MatchString(u) {
		return false
	}
	for _, m := range yearToken.FindAllStringSubmatch(u, -1) {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		// Only financial-year tokens (2025-26) count; dated upload folders
		// such as 2026-05 do not.
		if b == (a+1)%100 && fmt.Sprintf("20%02d-%02d", a, b) != year {
			return false
		}
	}
	return true
}

// mentionsYear requires the budget year somewhere in the document, which
// catches older documents whose file names carry no usable year.
func mentionsYear(text, year string) bool {
	return strings.Contains(text, year) || strings.Contains(text, strings.Replace(year, "-", "–", 1))
}

// verify confirms the quote sits under its own program's heading: on the
// cited page, "Program <code>" must appear shortly before the quote's opening
// words. It catches two-column layouts that pair a heading with the wrong text.
func verify(text string, o pbsdoc.Objective, code string) bool {
	pages := strings.Split(text, "\f")
	if o.Page < 1 || o.Page > len(pages) || code == "" {
		return false
	}
	flat := pbs.CleanText(pages[o.Page-1])
	words := strings.Fields(o.Text)
	start := strings.Join(words[:min(6, len(words))], " ")
	i := strings.Index(flat, start)
	if i < 0 {
		return false
	}
	h := strings.LastIndex(flat[:i], "Program "+code)
	if h < 0 {
		return false
	}
	// Nothing but the heading's name may sit between heading and quote.
	between := flat[h+len("Program "+code) : i]
	return len(between) < 250 && !strings.Contains(between, "Program ")
}

// pdfText downloads a PDF (cached) and returns `pdftotext -layout` output,
// pages separated by form feeds.
func pdfText(ctx context.Context, docURL, cache string) (string, error) {
	h := sha256.Sum256([]byte(docURL))
	path := filepath.Join(cache, hex.EncodeToString(h[:8])+".pdf")
	if _, err := os.Stat(path); err != nil {
		body, err := get(ctx, docURL)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(cache, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return "", err
		}
	}
	out, err := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", path, "-").Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return string(out), nil
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[1:], nil
}

func writeCSV(path string, header []string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Write(header)
	w.WriteAll(rows)
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
