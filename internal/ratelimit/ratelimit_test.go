package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func get(h http.Handler, remote, xff string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/contract/CN1", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLimitsEachClientAndRefills(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(http.NotFoundHandler(), 2, 3)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if w := get(l, "127.0.0.1:5000", "74.7.242.2"); w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited inside the burst", i+1)
		}
	}
	w := get(l, "127.0.0.1:5000", "74.7.242.2")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("4th request: status %d, Retry-After %q; want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
	if w := get(l, "127.0.0.1:5001", "203.0.113.9"); w.Code == http.StatusTooManyRequests {
		t.Error("another client behind Caddy shared the first one's limit")
	}
	now = now.Add(500 * time.Millisecond) // one request's worth at 2 a second
	if w := get(l, "127.0.0.1:5000", "74.7.242.2"); w.Code == http.StatusTooManyRequests {
		t.Error("bucket did not refill")
	}
	if w := get(l, "127.0.0.1:5000", "74.7.242.2"); w.Code != http.StatusTooManyRequests {
		t.Error("refill gave more than one request")
	}
}

func TestClientIP(t *testing.T) {
	for _, tc := range []struct{ remote, xff, want string }{
		{"127.0.0.1:5000", "74.7.242.2", "74.7.242.2"},
		{"127.0.0.1:5000", "10.0.0.1, 74.7.242.2", "74.7.242.2"},
		{"[::1]:5000", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1:5000", "", "127.0.0.1"},
		// Only Caddy, on loopback, is trusted to name the client.
		{"198.51.100.7:5000", "74.7.242.2", "198.51.100.7"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := clientIP(r); got != tc.want {
			t.Errorf("clientIP(%s, XFF %q) = %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}
