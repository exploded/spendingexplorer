package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestPager(t *testing.T) {
	for _, tc := range []struct {
		url        string
		total      int64
		ok         bool
		page       int
		off        int64
		first      int64
		last       int64
		prev, next string
	}{
		{"/grants/round/GO1", 120, true, 1, 0, 1, 50, "/grants/round/GO1", "/grants/round/GO1?page=2"},
		{"/grants/round/GO1?page=2", 120, true, 2, 50, 51, 100, "/grants/round/GO1", "/grants/round/GO1?page=3"},
		{"/grants/round/GO1?page=3", 120, true, 3, 100, 101, 120, "/grants/round/GO1?page=2", "/grants/round/GO1?page=4"},
		{"/grants/round/GO1?page=1", 120, true, 1, 0, 1, 50, "/grants/round/GO1", "/grants/round/GO1?page=2"},
		{"/suppliers?view=abn&page=2", 120, true, 2, 50, 51, 100, "/suppliers?view=abn", "/suppliers?page=3&view=abn"},
		{"/grants/round/GO1?page=4", 120, false, 0, 0, 0, 0, "", ""},
		{"/grants/round/GO1?page=0", 120, false, 0, 0, 0, 0, "", ""},
		{"/grants/round/GO1?page=x", 120, false, 0, 0, 0, 0, "", ""},
		{"/grants/round/GO1?page=2", 0, false, 0, 0, 0, 0, "", ""},
	} {
		p, ok := newPager(httptest.NewRequest("GET", tc.url, nil), 50, tc.total, "", "")
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.url, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if p.Page != tc.page || p.Off() != tc.off || p.First() != tc.first || p.Last() != tc.last {
			t.Errorf("%s: page %d off %d rows %d–%d, want page %d off %d rows %d–%d",
				tc.url, p.Page, p.Off(), p.First(), p.Last(), tc.page, tc.off, tc.first, tc.last)
		}
		if p.Prev() != tc.prev || p.Next() != tc.next {
			t.Errorf("%s: prev %q next %q, want %q %q", tc.url, p.Prev(), p.Next(), tc.prev, tc.next)
		}
	}
}

func TestPagerAtKeepsOtherList(t *testing.T) {
	r := httptest.NewRequest("GET", "/signals/repeat-limited?entity=7&page=2&cpage=3", nil)
	p, ok := newPagerAt(r, "cpage", 100, 1000, "", "")
	if !ok || p.Page != 3 {
		t.Fatalf("page %d ok %v, want 3 true", p.Page, ok)
	}
	if got, want := p.Next(), "/signals/repeat-limited?cpage=4&entity=7&page=2"; got != want {
		t.Errorf("next = %q, want %q", got, want)
	}
	if got, want := csvURL(r, "/signals/repeat-limited/contracts.csv"), "/signals/repeat-limited/contracts.csv?entity=7"; got != want {
		t.Errorf("csv = %q, want %q", got, want)
	}
}
