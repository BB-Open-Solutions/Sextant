package api

import (
	"net/http"
	"strconv"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// paging.go: opt-in bounding for list endpoints (audit A2).
//
// WHY NOW AND NOT LATER. No list endpoint bounded its result, which is fine
// at today's scale and stops being fine at a municipality's. The reason to
// settle the shape before the 1.0 freeze rather than after is that
// afterwards it is only safe if it is opt-in - and by then somebody will
// have written a client that assumes the whole list arrives.
//
// THE SHAPE, and every part of it is chosen to be additive:
//
//   - Absent parameters mean EVERYTHING, exactly as before. A client written
//     against 1.0 never sees a difference.
//   - The response stays a BARE ARRAY. Wrapping it in {items, total} would
//     have been tidier and would have broken every existing caller, which is
//     the trade this design refuses.
//   - The total goes in X-Total-Count. A header is additive by construction:
//     a client that does not read it is unaffected.
//
// So a caller who wants pages gets them, a caller who does not is untouched,
// and nothing here has to change at v2.

// maxPageLimit caps what one request may ask for. Not a security control -
// the endpoints are authenticated and rate-limited - but a bound stops a
// caller asking for a page so large that serialising it is the expensive
// part of the request.
const maxPageLimit = 1000

// page describes a requested window. A zero limit means "no limit", which is
// the default and the pre-existing behaviour.
type page struct {
	limit  int
	offset int
}

// pageFrom reads limit/offset from the query.
//
// A malformed value is an ERROR rather than a silently ignored parameter.
// Ignoring it would serve the whole list to a caller who asked for ten and
// believes they got ten - and they would page through it forever.
func pageFrom(r *http.Request) (page, error) {
	var p page
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, &ports.ValidationError{Detail: "limit: a non-negative whole number"}
		}
		if n > maxPageLimit {
			return p, &ports.ValidationError{
				Detail: "limit: at most " + strconv.Itoa(maxPageLimit) + " per request",
			}
		}
		p.limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, &ports.ValidationError{Detail: "offset: a non-negative whole number"}
		}
		p.offset = n
	}
	return p, nil
}

// writeList writes a page of items and always reports the unpaged total.
//
// The total is set even when no paging was asked for, so a client can learn
// the size of a list without having to page it - and so that adding paging
// later needs no second request to find out whether it is worth it.
//
// An offset past the end is an empty array and not an error: paging off the
// end of a list that shrank between calls is ordinary, and answering 404
// would turn a race into a failure.
func writeList[T any](w http.ResponseWriter, r *http.Request, items []T) error {
	p, err := pageFrom(r)
	if err != nil {
		return err
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))

	if p.offset >= len(items) {
		writeJSON(w, http.StatusOK, []T{})
		return nil
	}
	items = items[p.offset:]
	if p.limit > 0 && p.limit < len(items) {
		items = items[:p.limit]
	}
	writeJSON(w, http.StatusOK, items)
	return nil
}
