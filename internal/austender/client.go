package austender

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const BaseURL = "https://api.tenders.gov.au/ocds"

// Client fetches pages one at a time, retrying with backoff, and caches raw
// responses (gzipped) so parsing changes never need a re-fetch.
type Client struct {
	HTTP     *http.Client
	CacheDir string // empty disables the cache
	Pause    time.Duration
	Refresh  bool // ignore cached copies
}

func NewClient(cacheDir string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 90 * time.Second}, CacheDir: cacheDir, Pause: 250 * time.Millisecond}
}

// Get returns the body for url, from the cache when present.
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	path := c.cachePath(url)
	if path != "" && !c.Refresh {
		if b, err := readGzip(path); err == nil {
			return b, nil
		}
	}
	var body []byte
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		body, err = c.fetch(ctx, url)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if path != "" {
		if err := writeGzip(path, body); err != nil {
			return nil, err
		}
	}
	time.Sleep(c.Pause)
	return body, nil
}

func (c *Client) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "spendingexplorer (+https://github.com/exploded/spendingexplorer)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) cachePath(url string) string {
	if c.CacheDir == "" {
		return ""
	}
	h := sha256.Sum256([]byte(url))
	return filepath.Join(c.CacheDir, hex.EncodeToString(h[:8])+".json.gz")
}

func readGzip(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

func writeGzip(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(data)
	zw.Close()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ByDatesURL is the first page of releases for contracts whose field
// (contractPublished, contractLastModified, contractStart or contractEnd)
// falls in [from, to).
func ByDatesURL(field string, from, to time.Time) string {
	return fmt.Sprintf("%s/findByDates/%s/%s/%s", BaseURL, field,
		from.UTC().Format("2006-01-02T15:04:05Z"), to.Add(-time.Second).UTC().Format("2006-01-02T15:04:05Z"))
}

// ByIDURL returns every release of a contract notice.
func ByIDURL(cnID string) string {
	return BaseURL + "/findById/" + cnID
}
