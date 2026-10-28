// Package ratelimit caps how fast one client can request pages, so a crawler
// can't take the site down. A client over the limit gets 429 with Retry-After,
// which crawlers treat as a signal to slow down.
package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// retryAfter is how many seconds a limited client is asked to wait.
const retryAfter = 10

// Limiter gives each client a bucket of burst requests that refills at rate
// requests a second.
type Limiter struct {
	next  http.Handler
	rate  float64
	burst float64
	now   func() time.Time

	mu        sync.Mutex
	clients   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

func New(next http.Handler, rate float64, burst int) *Limiter {
	return &Limiter{next: next, rate: rate, burst: float64(burst), now: time.Now, clients: map[string]*bucket{}}
}

func (l *Limiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !l.allow(clientIP(r)) {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		http.Error(w, "Too many requests. Please slow down.", http.StatusTooManyRequests)
		return
	}
	l.next.ServeHTTP(w, r)
}

func (l *Limiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// A client whose bucket has refilled is no different from a new one.
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	if now.Sub(l.lastSweep) > full {
		for k, b := range l.clients {
			if now.Sub(b.seen) > full {
				delete(l.clients, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.clients[client]
	if !ok {
		b = &bucket{tokens: l.burst}
		l.clients[client] = b
	} else {
		b.tokens = min(l.burst, b.tokens+now.Sub(b.seen).Seconds()*l.rate)
	}
	b.seen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP is the address Caddy reports in X-Forwarded-For when the request
// came through it on loopback, otherwise the connection's own address. Caddy
// appends the client it saw, so the last entry is the one it vouches for.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(xff[strings.LastIndex(xff, ",")+1:])
		}
	}
	return host
}
