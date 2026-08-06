package api

import (
	"log/slog"
	"sync"
	"time"
)

// bridge_notice.go: the throttled warning both bridge-token paths emit.
//
// The shared token (check-in and station report, ADR 0008) is a migration
// path. Turning it off has to be a measurement rather than a guess, so every
// subject still leaning on it must be visible in the log - but a device checks
// in every thirty seconds, and a warning that arrives 2880 times a day is one
// nobody reads. Hourly per subject is often enough to answer "is anything
// still using this" and rare enough to stay a warning.
//
// Shared by the two endpoints so they cannot drift into warning at different
// rates, or one of them into not warning at all.

// bridgeNoticeEvery is how often one subject may draw the warning.
const bridgeNoticeEvery = time.Hour

// bridgeNotice throttles the warning per subject. The zero value is ready to
// use; it is embedded in the API structs by value, so it must not be copied
// after first use (neither struct is).
type bridgeNotice struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// warn logs msg for subject at most once per bridgeNoticeEvery. now may be nil
// (the tests' injected clock), in which case the wall clock is used. why and
// remedy are logged as their own fields: an operator meeting this line for the
// first time should not have to find the code to learn what to do about it.
func (b *bridgeNotice) warn(log *slog.Logger, now func() time.Time, msg, subjectKey, subject, why, remedy string) {
	if now == nil {
		now = time.Now
	}
	t := now()
	b.mu.Lock()
	last, seen := b.seen[subject]
	fresh := !seen || t.Sub(last) >= bridgeNoticeEvery
	if fresh {
		if b.seen == nil {
			b.seen = map[string]time.Time{}
		}
		b.seen[subject] = t
	}
	b.mu.Unlock()
	if fresh {
		log.Warn(msg, subjectKey, subject, "why", why, "remedy", remedy)
	}
}
