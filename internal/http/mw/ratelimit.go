package mw

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// clientIP returns the key a per-client limiter buckets on. Behind a trusted
// reverse proxy (the deployment's TLS-terminating ingress) r.RemoteAddr is the
// proxy's address - the same for every client, which would collapse the whole
// fleet into one bucket. When trustProxy is set we instead take the rightmost
// X-Forwarded-For entry: that is the peer address the trusted proxy actually
// observed, so a client cannot rotate a spoofed left-hand XFF to dodge the
// limit. With no proxy (direct/loopback) we must NOT trust XFF - it is fully
// client-controlled - so we key on RemoteAddr.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// RateLimit guards brute-forceable endpoints (login, token auth, check-in)
// with a per-client token bucket. Clients are keyed by IP (see clientIP for
// the trusted-proxy handling); the map is pruned so it cannot grow unbounded.
func RateLimit(rps rate.Limit, burst int, trustProxy bool) Middleware {
	type client struct {
		lim  *rate.Limiter
		seen time.Time
	}
	var (
		mu      sync.Mutex
		clients = map[string]*client{}
	)
	// Prune stale entries periodically (amortized on request handling).
	prune := func(now time.Time) {
		for k, c := range clients {
			if now.Sub(c.seen) > 10*time.Minute {
				delete(clients, k)
			}
		}
	}
	var lastPrune time.Time

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trustProxy)
			now := time.Now()
			mu.Lock()
			if now.Sub(lastPrune) > time.Minute {
				prune(now)
				lastPrune = now
			}
			c, ok := clients[ip]
			if !ok {
				c = &client{lim: rate.NewLimiter(rps, burst)}
				clients[ip] = c
			}
			c.seen = now
			allow := c.lim.Allow()
			mu.Unlock()
			if !allow {
				w.Header().Set("Retry-After", "10")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
