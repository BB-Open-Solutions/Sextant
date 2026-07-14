package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// CachedDirectory wraps a directory with a short-lived, per-query cache. A page
// that lists IdP groups (groups, access) then does not dial the directory on
// every load; and, crucially, an UNREACHABLE directory is dialled at most once
// per TTL instead of stalling every request on the dial timeout. Both results
// and errors are cached, so a down directory degrades to a fast "unavailable"
// rather than a slow one.
type CachedDirectory struct {
	inner ports.Directory
	ttl   time.Duration
	clock ports.Clock

	mu    sync.Mutex
	cache map[string]dirEntry
}

type dirEntry struct {
	groups []ports.DirectoryGroup
	err    error
	at     time.Time
}

// NewCachedDirectory wraps inner, caching each query's outcome for ttl.
func NewCachedDirectory(inner ports.Directory, ttl time.Duration, clock ports.Clock) *CachedDirectory {
	return &CachedDirectory{inner: inner, ttl: ttl, clock: clock, cache: map[string]dirEntry{}}
}

// ListGroups serves a fresh cache entry when one exists, otherwise dials the
// underlying directory and caches the outcome.
func (d *CachedDirectory) ListGroups(ctx context.Context, query string) ([]ports.DirectoryGroup, error) {
	now := d.clock.Now()
	d.mu.Lock()
	if e, ok := d.cache[query]; ok && now.Sub(e.at) < d.ttl {
		d.mu.Unlock()
		return e.groups, e.err
	}
	d.mu.Unlock()

	// Dial outside the lock so a slow directory does not serialize every
	// request behind the mutex.
	groups, err := d.inner.ListGroups(ctx, query)

	// A cancelled or deadline-exceeded error belongs to this caller's context,
	// not to the directory's health - caching it would poison every other
	// caller's result for the rest of the TTL, even a fresh one with a healthy
	// context. Let the next call redial.
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return groups, err
	}

	d.mu.Lock()
	d.cache[query] = dirEntry{groups: groups, err: err, at: now}
	d.mu.Unlock()
	return groups, err
}

// refresh always dials the directory and rewrites the cache entry, ignoring the
// current entry's freshness - the warmer uses it to keep the cache hot so page
// requests never dial. A context error is not cached (it is the warmer's, not
// the directory's).
func (d *CachedDirectory) refresh(ctx context.Context, query string) {
	groups, err := d.inner.ListGroups(ctx, query)
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}
	d.mu.Lock()
	d.cache[query] = dirEntry{groups: groups, err: err, at: d.clock.Now()}
	d.mu.Unlock()
}

// WarmLoop keeps the common ListGroups("") query hot in the background so the
// groups and access pages serve from cache instead of paying the LDAP dial on
// the first load after each TTL. It refreshes once immediately, then every
// ttl/2 until ctx is done; each fetch is bounded so a hung directory cannot
// stall the loop. Run it in a goroutine at wiring time when a directory is
// configured.
func (d *CachedDirectory) WarmLoop(ctx context.Context) {
	interval := d.ttl / 2
	if interval <= 0 {
		interval = 30 * time.Second
	}
	warm := func() {
		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.refresh(fctx, "")
	}
	warm()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			warm()
		}
	}
}
