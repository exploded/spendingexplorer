package handlers

import (
	"net/http"
	"strconv"

	"spendingexplorer/internal/export"
)

type dataPage struct {
	Period   period
	Manifest *export.Manifest
	Building bool // a build is under way; Manifest, if any, is the previous one
	SQLite   export.File
	CSV      export.File
}

func (h *Handler) data(w http.ResponseWriter, r *http.Request) {
	p, err := h.period(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	// The page changes when a build finishes, which the page cache can't see.
	w.Header().Set("Cache-Control", "no-store")
	page := dataPage{Period: p}
	page.Manifest, page.Building = h.downloads.Current()
	if page.Manifest != nil {
		page.SQLite, _ = page.Manifest.File(export.SQLiteFile)
		page.CSV, _ = page.Manifest.File(export.CSVFile)
	}
	h.render(w, "data.html", page)
}

// megabytes is a file size for people, such as "97 MB".
func megabytes(n int64) string {
	if n < 1<<20 {
		return "under 1 MB"
	}
	return strconv.FormatInt((n+1<<19)>>20, 10) + " MB"
}

// count groups a number's digits in thousands, such as 256,164.
func count(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
