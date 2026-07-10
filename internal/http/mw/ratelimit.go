package mw

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimit guards brute-forceable endpoints (login, token auth, check-in)
// with a per-client token bucket. Clients are keyed by IP; the map is
// pruned so it cannot grow without bound.
func RateLimit(rps rate.Limit, burst int) Middleware {
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
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
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
