package oidc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestHasGroupsOverage(t *testing.T) {
	over := map[string]any{
		"_claim_names":   map[string]any{"groups": "src1"},
		"_claim_sources": map[string]any{"src1": map[string]any{"endpoint": "https://graph..."}},
	}
	if !hasGroupsOverage(over) {
		t.Error("overage marker not detected")
	}
	if hasGroupsOverage(map[string]any{"groups": []any{"a"}}) {
		t.Error("normal claims flagged as overage")
	}
	if hasGroupsOverage(map[string]any{"_claim_names": map[string]any{"other": "x"}}) {
		t.Error("unrelated claim_names flagged")
	}
	if hasGroupsOverage(map[string]any{}) {
		t.Error("empty claims flagged")
	}
}

// TestFetchGroupsFromGraphPaging: the whole point of overage is >150
// groups, so paging via @odata.nextLink must work.
func TestFetchGroupsFromGraphPaging(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/page1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-123" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, `{"value":[{"id":"g-1","displayName":"dawo-beheer"},{"id":"g-2","displayName":"dawo-support"}],"@odata.nextLink":"%s/page2"}`, srvURL)
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"value":[{"id":"g-3","displayName":"auditors"},{"id":"g-1","displayName":"dawo-beheer"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	got, err := fetchGroupsFromGraph(context.Background(), srv.Client(), srv.URL+"/page1", "at-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"g-1", "dawo-beheer", "g-2", "dawo-support", "g-3", "auditors"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	// Deduped: g-1/dawo-beheer appear once despite the second page.
	if len(got) != 6 {
		t.Errorf("got %d entries, want 6 deduped: %v", len(got), got)
	}
}

func TestFetchGroupsFromGraphErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"message":"insufficient privileges"}}`)
	}))
	defer srv.Close()
	if _, err := fetchGroupsFromGraph(context.Background(), srv.Client(), srv.URL, "at"); err == nil {
		t.Fatal("graph 403 not surfaced")
	}

	// A nextLink loop must terminate (page bound), not hang.
	var loopURL string
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"value":[],"@odata.nextLink":"%s"}`, loopURL)
	}))
	defer loop.Close()
	loopURL = loop.URL
	if _, err := fetchGroupsFromGraph(context.Background(), loop.Client(), loop.URL, "at"); err != nil {
		t.Fatalf("bounded loop errored: %v", err)
	}
}
