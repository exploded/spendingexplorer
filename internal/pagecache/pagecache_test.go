package pagecache

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// counter serves an HTML page that says how many times it has rendered.
type counter struct{ n int }

func (c *counter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.n++
	if r.URL.Path == "/missing" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/live" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(strings.Repeat("x", 10) + r.URL.RequestURI() + r.Header.Get("HX-Target")))
}

func get(h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCachesPagesUntilDatabaseChanges(t *testing.T) {
	db := filepath.Join(t.TempDir(), "spending.db")
	os.WriteFile(db, []byte("a"), 0o644)
	next := &counter{}
	c := New(next, 1<<20, db, db+"-wal")

	first := get(c, "/entity/1")
	if second := get(c, "/entity/1"); second.Body.String() != first.Body.String() || next.n != 1 {
		t.Fatalf("second request rendered again (renders = %d)", next.n)
	}
	get(c, "/entity/1?sort=size")
	get(c, "/entity/1", "HX-Request", "true", "HX-Target", "x")
	if next.n != 3 {
		t.Errorf("query string and htmx target should be cached apart: renders = %d, want 3", next.n)
	}

	os.WriteFile(db+"-wal", []byte("b"), 0o644)
	get(c, "/entity/1")
	if next.n != 4 {
		t.Errorf("a new WAL file should empty the cache: renders = %d, want 4", next.n)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(db, later, later)
	get(c, "/entity/1")
	if next.n != 5 {
		t.Errorf("a rewritten database should empty the cache: renders = %d, want 5", next.n)
	}
}

func TestDoesNotCacheErrors(t *testing.T) {
	next := &counter{}
	c := New(next, 1<<20)
	get(c, "/missing")
	if w := get(c, "/missing"); w.Code != http.StatusNotFound || next.n != 2 {
		t.Errorf("got status %d after %d renders, want 404 after 2", w.Code, next.n)
	}
}

func TestDoesNotCacheNoStore(t *testing.T) {
	next := &counter{}
	c := New(next, 1<<20)
	get(c, "/live")
	if w := get(c, "/live"); w.Header().Get("Cache-Control") != "no-store" || next.n != 2 {
		t.Errorf("got Cache-Control %q after %d renders, want no-store after 2", w.Header().Get("Cache-Control"), next.n)
	}
}

func TestRevalidatesWithETag(t *testing.T) {
	c := New(&counter{}, 1<<20)
	w := get(c, "/")
	etag := w.Header().Get("ETag")
	if etag == "" || !strings.Contains(w.Header().Get("Cache-Control"), "s-maxage") {
		t.Fatalf("headers = %v", w.Header())
	}
	if w := get(c, "/", "If-None-Match", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("got status %d with %d bytes, want 304 and no body", w.Code, w.Body.Len())
	}
	if w := get(c, "/", "HX-Request", "true"); !strings.HasPrefix(w.Header().Get("Cache-Control"), "private") {
		t.Errorf("htmx response Cache-Control = %q, want private", w.Header().Get("Cache-Control"))
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	next := &counter{}
	c := New(next, 30) // each body is 12 bytes, so two fit
	get(c, "/a")
	get(c, "/b")
	get(c, "/a") // /a is now the most recently used
	get(c, "/c") // evicts /b
	get(c, "/a")
	if next.n != 3 {
		t.Errorf("/a should have stayed cached: renders = %d, want 3", next.n)
	}
	get(c, "/b")
	if next.n != 4 {
		t.Errorf("/b should have been evicted: renders = %d, want 4", next.n)
	}
}
