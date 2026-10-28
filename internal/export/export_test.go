package export

import (
	"archive/zip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spendingexplorer/internal/db"
)

func TestBuildsReusesAndRebuilds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spending.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`INSERT INTO source_document (title, publisher, url, file_name, sha256, doc_type, budget_year)
		VALUES ('Doc', 'Finance', 'https://example.com', 'doc.csv', 'abc', 'pbs_line_items', '2026-27');
		INSERT INTO load_run (command, source_document_id, rows_read, rows_written, finished_at)
		VALUES ('load-pbs', 1, 1, 1, '2026-10-28 02:38:10');
		INSERT INTO supplier (key, name) VALUES ('acme', 'Acme, "the" Pty Ltd')`); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "downloads")
	s := New(conn, out, path, path+"-wal")
	if err := s.Build(); err != nil {
		t.Fatal(err)
	}
	m, building := s.Current()
	if m == nil || building {
		t.Fatalf("after Build: manifest %v, building %v", m, building)
	}
	if want := time.Date(2026, 10, 28, 2, 38, 10, 0, time.UTC); !m.AsAt.Equal(want) {
		t.Errorf("AsAt = %v, want %v", m.AsAt, want)
	}
	if len(m.Files) != 2 || len(m.Tables) != len(tables) || m.Tables[0].About == "" {
		t.Fatalf("manifest = %+v", m)
	}

	// The CSV zip holds each table, quoted, with a byte-order mark.
	zr, err := zip.OpenReader(filepath.Join(out, m.Stamp, CSVFile))
	if err != nil {
		t.Fatal(err)
	}
	var suppliers string
	for _, f := range zr.File {
		if f.Name == "suppliers.csv" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			suppliers = string(b)
		}
	}
	zr.Close()
	if !strings.HasPrefix(suppliers, "\uFEFFsupplier_id,name,") || !strings.Contains(suppliers, `"Acme, ""the"" Pty Ltd"`) {
		t.Errorf("suppliers.csv = %q", suppliers)
	}

	// A second start on the same database reuses the build.
	again := New(conn, out, path, path+"-wal")
	if err := again.Build(); err != nil {
		t.Fatal(err)
	}
	if m2, _ := again.Current(); m2 == nil || m2.Built != m.Built {
		t.Errorf("rebuilt an unchanged database: %v", m2)
	}

	// A changed database gets a new build, and the old one is removed.
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	if err := again.Build(); err != nil {
		t.Fatal(err)
	}
	m3, _ := again.Current()
	if m3.Stamp == m.Stamp {
		t.Fatal("stamp did not change")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 1 || entries[0].Name() != m3.Stamp {
		t.Errorf("downloads dir holds %v, want only %s", entries, m3.Stamp)
	}

	// Files are served by name; anything else is not found.
	mux := http.NewServeMux()
	mux.Handle("GET /data/{file}", again)
	for path, code := range map[string]int{"/data/" + CSVFile: 200, "/data/manifest.json": 404} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code {
			t.Errorf("GET %s = %d, want %d", path, w.Code, code)
		}
	}
}
