package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// erasure_page_test.go covers the wiring of the art. 17 surface. The service
// has its own tests; what is asserted here is what only the page can get
// wrong - that a preview does not delete, that the confirm step is a second
// act, and that the page never renders a clean bill of health.

type pageErasureStore struct {
	erased bool
	counts ports.PersonalDataCounts
}

func (m *pageErasureStore) CountPersonalData(context.Context, string, string, string) (ports.PersonalDataCounts, error) {
	return m.counts, nil
}
func (m *pageErasureStore) ErasePersonalData(context.Context, string, string, string) (ports.PersonalDataCounts, error) {
	m.erased = true
	return m.counts, nil
}

func newErasureConsole(t *testing.T, u identity.User) (*httptest.Server, *pageErasureStore) {
	t.Helper()
	store := &pageErasureStore{counts: ports.PersonalDataCounts{SeenUser: 1, Notifications: 3}}
	svc := app.NewErasureService(store, nil, "default", nil)
	srv, err := web.New(web.Services{Config: newForgeConfig(t), Erasure: svc},
		scopedSessions{u}, true, nil, nil, []string{"owners"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, store
}

func TestErasurePageIsOwnerOnly(t *testing.T) {
	ts, _ := newErasureConsole(t, identity.User{Subject: "u", Groups: []string{"someone-else"}})
	resp, err := http.Get(ts.URL + "/org/erasure")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-owner GET /org/erasure = %d, want 403", resp.StatusCode)
	}
}

// TestPreviewDoesNotErase is the two-act design. A preview that deleted
// would make the confirm step theatre.
func TestPreviewDoesNotErase(t *testing.T) {
	ts, store := newErasureConsole(t, identity.User{Subject: "u", Groups: []string{"owners"}})
	resp, err := client().PostForm(ts.URL+"/org/erasure/preview", url.Values{
		"csrf": {"csrf"}, "subject": {"sub-1"}, "username": {"ada"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if store.erased {
		t.Fatal("the preview deleted data")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("preview = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)
	// It has to show the counts, or an operator confirms blind.
	if !strings.Contains(page, "3") {
		t.Error("the preview does not show what would be removed")
	}
	// And it must carry both identifiers into the confirm form, so what is
	// erased is what was just read.
	for _, want := range []string{`name="subject" value="sub-1"`, `name="username" value="ada"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the confirm form does not carry %s forward", want)
		}
	}
}

// TestThePageAlwaysSaysWhatSurvives: the whole reason this feature is safe
// to offer. An operator answers a data subject from this page.
func TestThePageAlwaysSaysWhatSurvives(t *testing.T) {
	ts, _ := newErasureConsole(t, identity.User{Subject: "u", Groups: []string{"owners"}})
	for _, path := range []string{"/org/erasure/preview", "/org/erasure/confirm"} {
		t.Run(path, func(t *testing.T) {
			resp, err := client().PostForm(ts.URL+path, url.Values{
				"csrf": {"csrf"}, "subject": {"sub-1"}, "username": {"ada"},
			})
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			page := string(body)
			if resp.StatusCode != 200 {
				t.Fatalf("%s = %d", path, resp.StatusCode)
			}
			if !strings.Contains(page, "git history") {
				t.Error("the page does not say the git history still names this person")
			}
			if !strings.Contains(strings.ToLower(page), "diagnostics") {
				t.Error("the page does not mention diagnostics bundles")
			}
		})
	}
}

func TestConfirmActuallyErases(t *testing.T) {
	ts, store := newErasureConsole(t, identity.User{Subject: "u", Groups: []string{"owners"}})
	resp, err := client().PostForm(ts.URL+"/org/erasure/confirm", url.Values{
		"csrf": {"csrf"}, "subject": {"sub-1"}, "username": {"ada"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("confirm = %d", resp.StatusCode)
	}
	if !store.erased {
		t.Error("the confirm step did not erase anything")
	}
}

// TestErasureWithNoIdentifierIsRefused: a blank run is the dangerous one -
// an empty subject would match rows whose subject column is empty.
func TestErasureWithNoIdentifierIsRefused(t *testing.T) {
	ts, store := newErasureConsole(t, identity.User{Subject: "u", Groups: []string{"owners"}})
	resp, err := client().PostForm(ts.URL+"/org/erasure/confirm", url.Values{
		"csrf": {"csrf"}, "subject": {""}, "username": {"  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Errorf("a blank erasure was accepted")
	}
	// And refused for the RIGHT reason. A 403 from CSRF or authorisation
	// would make this test pass while proving nothing about blank input.
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("refused with 403, which is authorisation rather than the blank check: %s", body)
	}
	if store.erased {
		t.Error("a blank erasure reached the store")
	}
}
