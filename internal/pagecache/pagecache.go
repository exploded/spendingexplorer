// Package pagecache keeps rendered pages in memory. The web app only reads its
// database, so a page changes only when a loader rewrites the database file;
// the cache empties itself when that happens.
package pagecache

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// sharedMaxAge is how long a shared cache in front of the app (Cloudflare)
// may serve a page without asking again. Browsers always revalidate.
const sharedMaxAge = 300

type Cache struct {
	next     http.Handler
	files    []string // database files whose changes empty the cache
	maxBytes int

	mu      sync.Mutex
	stamp   string
	entries map[string]*list.Element
	lru     *list.List // front is most recently used
	bytes   int
}

type entry struct {
	key    string
	header http.Header
	body   []byte
	etag   string
}

// New caches next's successful HTML responses to GET requests, up to maxBytes
// of bodies. Changing any of files (the database and its WAL) empties it.
func New(next http.Handler, maxBytes int, files ...string) *Cache {
	return &Cache{next: next, files: files, maxBytes: maxBytes, entries: map[string]*list.Element{}, lru: list.New()}
}

func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		c.next.ServeHTTP(w, r)
		return
	}
	// htmx requests can get part of a page, so they are cached apart.
	key := r.URL.RequestURI() + "\x00" + r.Header.Get("HX-Request") + "\x00" + r.Header.Get("HX-Target")
	stamp := c.fileStamp()
	if e := c.get(key, stamp); e != nil {
		serve(w, r, e)
		return
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	c.next.ServeHTTP(rec, r)
	// A page that depends on more than the database says so with no-store.
	if rec.status != http.StatusOK || !strings.HasPrefix(rec.header.Get("Content-Type"), "text/html") ||
		strings.Contains(rec.header.Get("Cache-Control"), "no-store") {
		rec.copyTo(w)
		return
	}
	sum := sha256.Sum256(rec.body.Bytes())
	e := &entry{key: key, header: rec.header, body: rec.body.Bytes(), etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	if r.Header.Get("HX-Request") != "" {
		// Cloudflare ignores Vary, so keep page fragments out of shared caches.
		e.header.Set("Cache-Control", "private, no-cache")
	} else {
		e.header.Set("Cache-Control", fmt.Sprintf("public, max-age=0, s-maxage=%d", sharedMaxAge))
	}
	e.header.Set("Vary", "HX-Request, HX-Target")
	e.header.Set("ETag", e.etag)
	c.put(e, stamp)
	serve(w, r, e)
}

func (c *Cache) get(key, stamp string) *entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stamp != c.stamp {
		c.entries, c.bytes, c.stamp = map[string]*list.Element{}, 0, stamp
		c.lru.Init()
		return nil
	}
	el, ok := c.entries[key]
	if !ok {
		return nil
	}
	c.lru.MoveToFront(el)
	return el.Value.(*entry)
}

func (c *Cache) put(e *entry, stamp string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A page rendered while the database changed may be out of date.
	if stamp != c.stamp || len(e.body) > c.maxBytes {
		return
	}
	if old, ok := c.entries[e.key]; ok {
		c.bytes -= len(old.Value.(*entry).body)
		c.lru.Remove(old)
	}
	c.entries[e.key] = c.lru.PushFront(e)
	c.bytes += len(e.body)
	for c.bytes > c.maxBytes {
		old := c.lru.Remove(c.lru.Back()).(*entry)
		delete(c.entries, old.key)
		c.bytes -= len(old.body)
	}
}

// fileStamp identifies the current state of the database files by size and
// modification time. A missing file (such as an absent WAL) counts as a state.
func (c *Cache) fileStamp() string {
	var b strings.Builder
	for _, f := range c.files {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%d.%d;", fi.Size(), fi.ModTime().UnixNano())
		} else {
			b.WriteString("-;")
		}
	}
	return b.String()
}

func serve(w http.ResponseWriter, r *http.Request, e *entry) {
	h := w.Header()
	for k, v := range e.header {
		h[k] = v
	}
	if r.Header.Get("If-None-Match") == e.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(e.body)
}

// recorder holds a response so it can be cached before it is sent.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(status int)      { r.status = status }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }

func (r *recorder) copyTo(w http.ResponseWriter) {
	h := w.Header()
	for k, v := range r.header {
		h[k] = v
	}
	w.WriteHeader(r.status)
	w.Write(r.body.Bytes())
}
