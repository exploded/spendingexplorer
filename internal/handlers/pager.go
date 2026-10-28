package handlers

import (
	"net/http"
	"net/url"
	"strconv"
)

// pager splits a long list into pages of Size rows, chosen by ?page=. Page
// links keep the request's other parameters, such as view and entity.
type pager struct {
	Page   int
	Size   int
	Total  int64
	CSV    string // the whole list as CSV
	Target string // id of the element page links swap
	param  string
	path   string
	query  url.Values
}

// newPager reads ?page= for a list of total rows. It reports false when the
// parameter is not a page of the list, which callers answer with 404 so
// crawlers can't wander through endless empty pages.
func newPager(r *http.Request, size int, total int64, csv, target string) (pager, bool) {
	return newPagerAt(r, "page", size, total, csv, target)
}

// newPagerAt is newPager for a page with two lists, which need a parameter
// each.
func newPagerAt(r *http.Request, param string, size int, total int64, csv, target string) (pager, bool) {
	p := pager{Page: 1, Size: size, Total: total, CSV: csv, Target: target, param: param, path: r.URL.Path, query: r.URL.Query()}
	if s := p.query.Get(param); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > p.Pages() {
			return p, false
		}
		p.Page = n
	}
	return p, true
}

// Lim and Off are the query's LIMIT and OFFSET for the current page.
func (p pager) Lim() int64 { return int64(p.Size) }
func (p pager) Off() int64 { return int64((p.Page - 1) * p.Size) }

func (p pager) Pages() int { return max(1, int((p.Total+int64(p.Size)-1)/int64(p.Size))) }

// First and Last number the rows on this page, from 1.
func (p pager) First() int64 { return p.Off() + 1 }
func (p pager) Last() int64  { return min(p.Off()+int64(p.Size), p.Total) }

// Rank numbers the page's row i (from 0) within the whole list.
func (p pager) Rank(i int) int64 { return p.Off() + int64(i) + 1 }

func (p pager) Prev() string { return p.url(p.Page - 1) }
func (p pager) Next() string { return p.url(p.Page + 1) }

func (p pager) HasPrev() bool { return p.Page > 1 }
func (p pager) HasNext() bool { return p.Page < p.Pages() }

// url links to page n. Page 1 has no page parameter, so it shares its URL
// with the page as linked from elsewhere.
func (p pager) url(n int) string {
	q := url.Values{}
	for k, v := range p.query {
		q[k] = v
	}
	if n > 1 {
		q.Set(p.param, strconv.Itoa(n))
	} else {
		q.Del(p.param)
	}
	if len(q) == 0 {
		return p.path
	}
	return p.path + "?" + q.Encode()
}

// csvURL is a list's CSV download, keeping the parameters that choose the
// list (such as view and entity) but not the page or a search.
func csvURL(r *http.Request, path string) string {
	q := r.URL.Query()
	for _, k := range []string{"page", "cpage", "q"} {
		q.Del(k)
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}
