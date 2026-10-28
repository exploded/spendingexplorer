package handlers

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/signals"
)

// sitemapSize is the most URLs one sitemap file may list.
const sitemapSize = 50000

// sitemapIndex lists the sitemap files: one for the explorer's pages, one for
// suppliers and as many as the contracts need.
func (h *Handler) sitemapIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asAt, err := h.q.DataAsAt(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	contracts, err := h.q.SitemapContractCount(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	files := []string{"pages.xml", "suppliers.xml"}
	for i := int64(1); i <= (contracts+sitemapSize-1)/sitemapSize; i++ {
		files = append(files, "contracts-"+strconv.FormatInt(i, 10)+".xml")
	}
	var b bytes.Buffer
	b.WriteString(xml.Header + `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, f := range files {
		b.WriteString("<sitemap><loc>")
		xml.EscapeText(&b, []byte(siteURL(r)+"/sitemap/"+f))
		b.WriteString("</loc>" + lastmod(asAt) + "</sitemap>\n")
	}
	b.WriteString("</sitemapindex>\n")
	writeXML(w, b.Bytes())
}

func (h *Handler) sitemapFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := r.PathValue("file")
	s := &urlset{base: siteURL(r)}
	switch {
	case file == "pages.xml":
		if err := h.sitemapPages(r, s); err != nil {
			h.fail(w, err)
			return
		}
	case file == "suppliers.xml":
		sups, err := h.q.SitemapSuppliers(ctx)
		if err != nil {
			h.fail(w, err)
			return
		}
		for _, sp := range sups {
			s.add("/supplier/"+strconv.FormatInt(sp.ID, 10), sp.LastRelease)
		}
	case strings.HasPrefix(file, "contracts-") && strings.HasSuffix(file, ".xml"):
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(file, "contracts-"), ".xml"), 10, 64)
		if err != nil || n < 1 {
			http.NotFound(w, r)
			return
		}
		cs, err := h.q.SitemapContracts(ctx, db.SitemapContractsParams{Limit: sitemapSize, Offset: (n - 1) * sitemapSize})
		if err != nil {
			h.fail(w, err)
			return
		}
		if len(cs) == 0 {
			http.NotFound(w, r)
			return
		}
		for _, c := range cs {
			s.add("/contract/"+url.PathEscape(c.CnID), c.ReleaseDate)
		}
	default:
		http.NotFound(w, r)
		return
	}
	writeXML(w, s.bytes())
}

// sitemapPages lists every page other than suppliers and contracts. They all
// change when the data is reloaded, so each carries the load date.
func (h *Handler) sitemapPages(r *http.Request, s *urlset) error {
	ctx := r.Context()
	asAt, err := h.q.DataAsAt(ctx)
	if err != nil {
		return err
	}
	p, err := h.period(ctx)
	if err != nil {
		return err
	}
	for _, path := range []string{"/", "/suppliers", "/signals", "/investments", "/projects", "/about", "/methodology", "/sources", "/data"} {
		s.add(path, asAt)
	}
	for _, rule := range signals.Rules {
		s.add("/signals/"+url.PathEscape(rule.ID), asAt)
	}
	projects, err := h.q.SitemapProjects(ctx)
	if err != nil {
		return err
	}
	for _, k := range projects {
		s.add("/projects/"+url.PathEscape(k), asAt)
	}
	ports, err := h.q.SitemapPortfolios(ctx, p.Doc.ID)
	if err != nil {
		return err
	}
	for _, name := range ports {
		s.add("/portfolio/"+url.PathEscape(name), asAt)
	}
	entities, err := h.q.SitemapEntities(ctx, p.Doc.ID)
	if err != nil {
		return err
	}
	programs, err := h.q.SitemapPrograms(ctx, p.FY)
	if err != nil {
		return err
	}
	groups, err := h.q.SitemapGroups(ctx)
	if err != nil {
		return err
	}
	for _, l := range []struct {
		prefix string
		ids    []int64
	}{{"/entity/", entities}, {"/program/", programs}, {"/group/", groups}} {
		for _, id := range l.ids {
			s.add(l.prefix+strconv.FormatInt(id, 10), asAt)
		}
	}
	return nil
}

type urlset struct {
	base string
	b    bytes.Buffer
}

func (s *urlset) add(path, date string) {
	if s.b.Len() == 0 {
		s.b.WriteString(xml.Header + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	}
	s.b.WriteString("<url><loc>")
	xml.EscapeText(&s.b, []byte(s.base+path))
	s.b.WriteString("</loc>" + lastmod(date) + "</url>\n")
}

func (s *urlset) bytes() []byte {
	if s.b.Len() == 0 {
		s.add("/", "")
	}
	return append(s.b.Bytes(), "</urlset>\n"...)
}

// lastmod is the element for a YYYY-MM-DD date (or a longer timestamp, cut
// to its date), or nothing if there is no date.
func lastmod(date string) string {
	if len(date) < 10 {
		return ""
	}
	return fmt.Sprintf("<lastmod>%s</lastmod>", date[:10])
}

func writeXML(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write(b)
}

// siteURL is the scheme and host the request came in on. Caddy passes the
// original host and sets X-Forwarded-Proto.
func siteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
