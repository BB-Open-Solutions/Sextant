package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStaticETagRevalidation verifies embedded assets carry a content ETag and
// revalidate: a first GET returns the bytes with an ETag; a conditional GET
// carrying that ETag gets 304, so an unchanged asset is not re-sent but a
// changed one (different content hash) would be.
func TestStaticETagRevalidation(t *testing.T) {
	h := staticHandler()

	r1 := httptest.NewRequest("GET", "/static/app.css", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first GET: got %d, want 200", w1.Code)
	}
	et := w1.Header().Get("ETag")
	if et == "" {
		t.Fatal("no ETag on static asset")
	}
	if cc := w1.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}

	r2 := httptest.NewRequest("GET", "/static/app.css", nil)
	r2.Header.Set("If-None-Match", et)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("conditional GET: got %d, want 304", w2.Code)
	}
}

// TestStaticFontHasETag guards the font specifically: it is the asset most
// prone to stale-cache (stable filename, changes when the icon subset is
// regenerated), so it must revalidate.
func TestStaticFontHasETag(t *testing.T) {
	h := staticHandler()
	r := httptest.NewRequest("GET", "/static/fonts/material-symbols.woff2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("font GET: got %d, want 200", w.Code)
	}
	if w.Header().Get("ETag") == "" {
		t.Fatal("font served without an ETag; a regenerated subset would stay cached")
	}
}
