// Package export builds the downloads offered on the /data page: the whole
// database as SQLite, and CSVs of its main tables. Both come from one
// snapshot, so they always agree. The web app builds them when it starts on a
// database it has not exported yet, and serves the previous set meanwhile.
package export

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"spendingexplorer/internal/classify"
	"spendingexplorer/internal/db"
)

// version is part of every build's stamp. Bump it when the files' contents
// change, so the next start rebuilds them.
const version = "2"

const (
	SQLiteFile = "spending-sqlite.zip"
	CSVFile    = "spending-csv.zip"
)

type File struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Table struct {
	File  string `json:"file"`
	Rows  int64  `json:"rows"`
	About string `json:"-"` // filled from tables when a manifest is loaded
}

type Manifest struct {
	Stamp  string    `json:"stamp"`
	AsAt   time.Time `json:"as_at"` // when the data was last loaded
	Built  time.Time `json:"built"`
	Files  []File    `json:"files"`
	Tables []Table   `json:"tables"`
}

func (m *Manifest) File(name string) (File, bool) {
	for _, f := range m.Files {
		if f.Name == name {
			return f, true
		}
	}
	return File{}, false
}

// Store builds the downloads into dir, one directory per build, and serves
// the latest complete one.
type Store struct {
	src   *sql.DB
	dir   string
	files []string // database files whose state identifies a build

	mu       sync.RWMutex
	current  *Manifest
	building bool
}

// New exports src into dir. files are the database and its WAL: a change to
// either means the export is out of date.
func New(src *sql.DB, dir string, files ...string) *Store {
	return &Store{src: src, dir: dir, files: files}
}

// Current returns the latest complete build, or nil if there is none, and
// whether a newer one is being built.
func (s *Store) Current() (*Manifest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current, s.building
}

// Build makes the downloads for the database as it is now, unless they
// already exist. It takes a minute or so for the full database.
func (s *Store) Build() error {
	stamp := s.stamp()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if m := s.latest(); m != nil {
		s.mu.Lock()
		s.current = m
		s.mu.Unlock()
		if m.Stamp == stamp {
			return nil
		}
	}
	s.mu.Lock()
	s.building = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.building = false
		s.mu.Unlock()
	}()

	start := time.Now()
	m, err := s.build(stamp)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.current = m
	s.mu.Unlock()
	slog.Info("built downloads", "as_at", m.AsAt, "took", time.Since(start).Round(time.Second))

	// Older builds go once nothing points at them. A download still reading
	// one keeps its open file on Linux; on Windows removal fails and the next
	// build tries again.
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.Name() != stamp {
			os.RemoveAll(filepath.Join(s.dir, e.Name()))
		}
	}
	return nil
}

// stamp identifies the database's current state, like pagecache does.
func (s *Store) stamp() string {
	h := sha256.New()
	io.WriteString(h, version)
	for _, f := range s.files {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(h, ";%d.%d", fi.Size(), fi.ModTime().UnixNano())
		} else {
			io.WriteString(h, ";-")
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// latest returns the most recently built complete export in dir, if any.
func (s *Store) latest() *Manifest {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var best *Manifest
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(b, &m) != nil || m.Stamp != e.Name() {
			continue
		}
		if best == nil || m.Built.After(best.Built) {
			best = &m
		}
	}
	if best != nil {
		describe(best)
	}
	return best
}

func describe(m *Manifest) {
	about := map[string]string{}
	for _, t := range tables {
		about[t.file] = t.about
	}
	for i := range m.Tables {
		m.Tables[i].About = about[m.Tables[i].File]
	}
}

func (s *Store) build(stamp string) (m *Manifest, err error) {
	tmp := filepath.Join(s.dir, stamp+".tmp")
	os.RemoveAll(tmp)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()

	dbFile := filepath.Join(tmp, "spending.db")
	if err := db.Snapshot(s.src, dbFile); err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	snap, err := db.OpenReadOnly(dbFile)
	if err != nil {
		return nil, err
	}
	m = &Manifest{Stamp: stamp, Built: time.Now().UTC()}
	var asAt string
	if err := snap.QueryRow("SELECT COALESCE(MAX(finished_at), '') FROM load_run").Scan(&asAt); err != nil {
		snap.Close()
		return nil, err
	}
	m.AsAt, _ = time.Parse(time.DateTime, asAt)
	m.Tables, err = writeCSVs(snap, filepath.Join(tmp, CSVFile))
	snap.Close()
	if err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}
	if err := zipDatabase(dbFile, filepath.Join(tmp, SQLiteFile)); err != nil {
		return nil, fmt.Errorf("zip database: %w", err)
	}
	if err := os.Remove(dbFile); err != nil {
		return nil, err
	}

	for _, name := range []string{SQLiteFile, CSVFile} {
		f, err := describeFile(filepath.Join(tmp, name))
		if err != nil {
			return nil, err
		}
		m.Files = append(m.Files, f)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), b, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, stamp)); err != nil {
		return nil, err
	}
	describe(m)
	return m, nil
}

func describeFile(path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return File{}, err
	}
	return File{Name: filepath.Base(path), Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func zipDatabase(dbFile, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	if err := addReadme(zw); err != nil {
		return err
	}
	w, err := zw.Create("spending.db")
	if err != nil {
		return err
	}
	src, err := os.Open(dbFile)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := io.Copy(w, src); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func writeCSVs(conn *sql.DB, out string) ([]Table, error) {
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	if err := addReadme(zw); err != nil {
		return nil, err
	}
	var done []Table
	for _, t := range tables {
		w, err := zw.Create(t.file)
		if err != nil {
			return nil, err
		}
		n, err := writeCSV(w, conn, t.query, t.args...)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.file, err)
		}
		done = append(done, Table{File: t.file, Rows: n})
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return done, f.Close()
}

// writeCSV writes a query's rows with its column names as the header. The
// byte-order mark makes Excel read the file as UTF-8.
func writeCSV(w io.Writer, conn *sql.DB, query string, args ...any) (int64, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if _, err := io.WriteString(w, "\uFEFF"); err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	cw.Write(cols)
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	rec := make([]string, len(cols))
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for i, v := range vals {
			rec[i] = cell(v)
		}
		if err := cw.Write(rec); err != nil {
			return n, err
		}
		n++
	}
	cw.Flush()
	return n, errors.Join(rows.Err(), cw.Error())
}

func cell(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []byte:
		return string(v)
	case string:
		return v
	case time.Time:
		return v.UTC().Format(time.DateTime)
	default:
		return fmt.Sprint(v)
	}
}

func addReadme(zw *zip.Writer) error {
	w, err := zw.Create("README.txt")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, strings.ReplaceAll(readme, "\n", "\r\n"))
	return err
}

// ServeHTTP serves a file from the current build, by name.
func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m, _ := s.Current()
	if m == nil {
		http.Error(w, "The downloads are being prepared. Try again in a few minutes.", http.StatusServiceUnavailable)
		return
	}
	file, ok := m.File(r.PathValue("file"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.dir, m.Stamp, file.Name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	// The server's write timeout suits pages, not a large file on a slow link.
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("ETag", `"`+file.SHA256[:16]+`"`)
	w.Header().Set("Cache-Control", "public, max-age=0")
	http.ServeContent(w, r, file.Name, m.Built, f)
}

type table struct {
	file  string
	about string
	query string
	args  []any
}

// tables are the CSVs, in the order the /data page lists them. Amounts are
// whole dollars throughout.
var tables = []table{
	{
		file:  "contracts.csv",
		about: "Every AusTender contract notice, at its latest amendment. value_whole_of_life is the contract's value over its whole term, not annual spending. ai_category is an AI classification of professional services contracts and may be wrong.",
		query: `SELECT c.cn_id, c.ocid, a.name AS agency, a.entity_id, e.name AS entity,
  c.supplier_id, s.name AS supplier, s.abn AS supplier_abn, s.country AS supplier_country,
  s.postcode AS supplier_postcode, si.supplier_id IS NOT NULL AS supplier_may_be_individual,
  c.title, c.description, c.unspsc, c.method, c.method_detail, c.limited_reason, c.exemption,
  c.start_date, c.end_date, c.published_date, c.published_fy, c.tender_threshold,
  c.value AS value_whole_of_life, c.original_value, c.amendment_count, c.status,
  c.release_date AS last_release_date,
  (SELECT l.category FROM llm_classification l WHERE l.cn_id = c.cn_id AND l.task = ?
   ORDER BY l.prompt_version DESC LIMIT 1) AS ai_category
FROM contract_pub c
JOIN agency a ON a.id = c.agency_id
LEFT JOIN entity e ON e.id = a.entity_id
LEFT JOIN supplier s ON s.id = c.supplier_id
LEFT JOIN supplier_individual si ON si.supplier_id = c.supplier_id
ORDER BY c.published_date, c.cn_id`,
		args: []any{classify.Task},
	},
	{
		file:  "contract_amendments.csv",
		about: "Every published version of each contract notice, with the value it gave.",
		query: `SELECT cn_id, release_id, release_date, amendment, value FROM contract_release
ORDER BY cn_id, release_date, release_id`,
	},
	{
		file:  "contract_annual_estimates.csv",
		about: "Each contract's value spread evenly by day from start to end date, by financial year. An estimate, not reported spending.",
		query: `SELECT cn_id, fiscal_year, amount AS amount_estimate FROM contract_fy ORDER BY cn_id, fiscal_year`,
	},
	{
		file:  "suppliers.csv",
		about: "Suppliers named in contract notices, with their ABR entity type and corporate group. individual_basis is set when the supplier is, or may be, a person.",
		query: `SELECT s.id AS supplier_id, s.name, s.abn, s.country, s.postcode,
  si.basis AS individual_basis, ab.entity_type_text AS abr_entity_type, ab.status AS abr_status,
  gm.group_id, g.name AS group_name, gm.basis AS group_basis
FROM supplier s
LEFT JOIN supplier_individual si ON si.supplier_id = s.id
LEFT JOIN abr_entity ab ON ab.abn = s.abn AND s.abn <> ''
LEFT JOIN supplier_group_member gm ON gm.supplier_id = s.id
LEFT JOIN supplier_group g ON g.id = gm.group_id
ORDER BY s.id`,
	},
	{
		file:  "program_expenses.csv",
		about: "Portfolio Budget Statements program expense line items, by financial year. treatment says whether a line counts as an expense or was set aside to avoid double counting; treatment_rule names the rule, explained on the methodology page.",
		query: `SELECT pe.id, sd.title AS source_document, sd.budget_year AS publication, pe.source_table,
  pe.source_row, p.portfolio, p.entity_id, e.name AS entity, p.outcome, p.id AS program_id,
  p.name AS program, pe.fiscal_year, pe.estimate_type, pe.expense_kind, pe.line_item,
  pe.amount, pe.not_published, pe.treatment, pe.treatment_rule
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
JOIN entity e ON e.id = p.entity_id
JOIN source_document sd ON sd.id = pe.source_document_id
ORDER BY pe.id`,
	},
	{
		file:  "whole_of_government.csv",
		about: "Total expenses by function and by type, from Budget Paper No. 1 and the Final Budget Outcome.",
		query: `SELECT sd.title AS source_document, sd.budget_year AS publication, w.fiscal_year,
  w.estimate_type, w.lens, w.category, w.amount
FROM wog_expense w JOIN source_document sd ON sd.id = w.source_document_id
ORDER BY w.id`,
	},
	{
		file:  "subfunctions.csv",
		about: "Expenses by sub-function (Budget Paper No. 1, Appendix A), and the general revenue assistance itemised in Budget Paper No. 3.",
		query: `SELECT sd.title AS source_document, w.source_table, w.fiscal_year, w.estimate_type,
  w.function_name, w.parent, w.name, w.position, w.amount, w.note
FROM wog_subfunction w JOIN source_document sd ON sd.id = w.source_document_id
ORDER BY w.id`,
	},
	{
		file:  "loans_and_investments.csv",
		about: "Net cash flows from loans and equity provided for policy purposes, which are not expenses. Negative is net cash paid out.",
		query: `SELECT sd.title AS source_document, i.fiscal_year, i.estimate_type, i.item, i.amount, i.is_total
FROM policy_investment i JOIN source_document sd ON sd.id = i.source_document_id
ORDER BY sd.budget_year, i.fiscal_year, i.is_total, i.item`,
	},
	{
		file:  "grants.csv",
		about: "GrantConnect grant awards, at their latest version. value_whole_of_term is the award's total over its term.",
		query: `SELECT g.ga_id, g.version_id, g.agency_name AS agency, g.entity_id, e.name AS entity,
  g.recipient, g.individual AS recipient_may_be_individual, g.unnamed AS recipient_unnamed,
  g.activity, g.category, g.go_id, g.publish_date, g.start_date, g.end_date,
  g.value AS value_whole_of_term, g.one_off, g.aggregate
FROM grant_award g LEFT JOIN entity e ON e.id = g.entity_id
ORDER BY g.publish_date, g.ga_id`,
	},
	{
		file:  "grant_annual_estimates.csv",
		about: "Each grant's value spread evenly over its term, by financial year. An estimate, not reported spending.",
		query: `SELECT ga_id, fiscal_year, amount AS amount_estimate FROM grant_fy ORDER BY ga_id, fiscal_year`,
	},
	{
		file:  "signals.csv",
		about: "Contracts matching a \"worth a closer look\" rule, explained on the methodology page. A pattern worth checking, not a finding of wrongdoing. Suppliers who are, or may be, individuals are left out, as they are on the site's signal lists.",
		query: `SELECT s.rule_id, s.cn_id, a.name AS agency, s.supplier_id, sp.name AS supplier,
  s.fiscal_year, s.amount, s.measure
FROM signal s
JOIN agency a ON a.id = s.agency_id
LEFT JOIN supplier sp ON sp.id = s.supplier_id
WHERE s.supplier_id IS NULL OR s.supplier_id NOT IN (SELECT supplier_id FROM supplier_individual)
ORDER BY s.rule_id, s.cn_id`,
	},
	{
		file:  "sources.csv",
		about: "Every source file and API response the data was loaded from, with its SHA-256 fingerprint.",
		query: `SELECT id, title, publisher, url, file_name, sha256, doc_type, budget_year, retrieved_at
FROM source_document ORDER BY id`,
	},
}

const readme = `Federal Spending Explorer - data download
https://spending.mchugh.au/data

This is the data behind the Federal Spending Explorer, a top-down view of
Australian Government spending. The same notes are on the web page above.

Reading the figures
- Amounts are whole Australian dollars.
- Budget estimates and actuals are different series. Every amount carries its
  estimate type (budget, forward_estimate, estimated_actual or actual) and
  its source document.
- Contract and grant values are whole-of-life (or whole-of-term) values as
  published, not annual spending. The *_annual_estimates files spread each
  value evenly over its term; they are estimates.
- Program line items do not add up to the whole-of-government total. The
  difference is whole-of-government adjustments; see
  https://spending.mchugh.au/methodology#adjustments
- ai_category and AI-grouped corporate groups (group_basis = ai) come from a
  language model and may be wrong.
- Signals are patterns worth a closer look, not findings of wrongdoing.

Individuals
Some suppliers and grant recipients are people, such as sole traders. Their
names are as the government published them. The site leaves them out of
rankings and signal lists; please do the same. They are flagged by
supplier_may_be_individual, individual_basis and recipient_may_be_individual.

Licence and credit
Our own work on the data is licensed under Creative Commons Attribution 4.0
(CC BY 4.0, https://creativecommons.org/licenses/by/4.0/). That covers the
database as a whole, the AI classifications and groupings, the signals and
the annual estimates. The underlying figures come from AusTender,
GrantConnect, Portfolio Budget Statements, Budget Papers, the Transparency
Portal, the Australian Business Register and GLEIF, and keep their
publishers' licences; sources.csv and https://spending.mchugh.au/sources list
them. Please credit the original publishers and the Federal Spending Explorer
(spending.mchugh.au).

Files
- spending-sqlite.zip holds spending.db, the SQLite database the site runs on.
- spending-csv.zip holds CSVs of the main tables, UTF-8 with a byte-order mark
  so Excel reads them correctly.
`
