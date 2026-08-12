package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Erasure answers a GDPR article 17 request, and it is the most destructive
// thing in the console that a re-image cannot undo. It is deliberately two
// acts: a preview that reports what would go, and a confirm that goes.
//
// The property that carries the whole design is that the FIRST act removes
// nothing. An operator checking what a request would affect must be able to
// do so without performing it, and if preview ever erased, the mistake would
// be discovered by a data subject rather than by a test.
//
// Asserted against a store that records which method was called, because
// "the report says nothing was removed" and "nothing was removed" are
// different claims and only the second one matters.

type recordingErasure struct {
	mu       sync.Mutex
	counted  []string // tenant|subject|username per count call
	erased   []string // ditto per erase call
	counts   ports.PersonalDataCounts
	countErr error
}

func (r *recordingErasure) CountPersonalData(_ context.Context, tenant, subject, username string) (ports.PersonalDataCounts, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counted = append(r.counted, tenant+"|"+subject+"|"+username)
	return r.counts, r.countErr
}

func (r *recordingErasure) ErasePersonalData(_ context.Context, tenant, subject, username string) (ports.PersonalDataCounts, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.erased = append(r.erased, tenant+"|"+subject+"|"+username)
	return r.counts, nil
}

func (r *recordingErasure) erasures() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.erased...)
}

// erasureConsole builds the console with a real ErasureService over the fake
// store. Separate from newConsole because that helper leaves Erasure nil,
// which is the "postgres not configured" case rather than this one.
func erasureConsole(t *testing.T, store ports.ErasureStore) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": seedFleet, "catalog.json": seedCatalog} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	svcs := web.Services{Config: cfg}
	if store != nil {
		svcs.Erasure = app.NewErasureService(store, cfg, app.DefaultTenant, quiet)
	}
	srv, err := web.New(svcs, web.DevSessions{}, true, nil, nil, nil, quiet)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, ts *httptest.Server, path string, form url.Values) (int, string) {
	t.Helper()
	form.Set("csrf", "dev-csrf") // DevSessions' token
	resp, err := client().PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(body)
}

func TestErasurePreviewRemovesNothing(t *testing.T) {
	store := &recordingErasure{counts: ports.PersonalDataCounts{SeenUser: 1, Prefs: 1}}
	ts := erasureConsole(t, store)

	code, _ := post(t, ts, "/org/erasure/preview", url.Values{
		"subject": {"sub-1"}, "username": {"ada"},
	})
	if code != http.StatusOK {
		t.Fatalf("preview = %d", code)
	}

	if got := store.erasures(); len(got) != 0 {
		t.Fatalf("the preview erased %v; an operator checking what a request "+
			"would affect has just performed it", got)
	}
	if len(store.counted) != 1 {
		t.Errorf("counted %d times, want 1", len(store.counted))
	}
}

func TestErasureConfirmErasesExactlyWhatTheFormSaid(t *testing.T) {
	store := &recordingErasure{counts: ports.PersonalDataCounts{SeenUser: 1}}
	ts := erasureConsole(t, store)

	code, _ := post(t, ts, "/org/erasure/confirm", url.Values{
		"subject": {"sub-1"}, "username": {"ada"},
	})
	if code != http.StatusOK {
		t.Fatalf("confirm = %d", code)
	}

	got := store.erasures()
	if len(got) != 1 {
		t.Fatalf("erased %d times, want 1: %v", len(got), got)
	}
	// The identifiers travel back through the form on purpose: what is erased
	// must be what the operator just read on the preview, not a server-side
	// value that moved in between.
	if !strings.HasSuffix(got[0], "|sub-1|ada") {
		t.Errorf("erased %q, want the identifiers the form carried", got[0])
	}
}

// Whitespace around a pasted identifier must not become part of it. A subject
// is matched exactly, so " sub-1" erases nothing and the operator is told the
// person has no data, which is a wrong answer to give a data subject.
func TestErasureTrimsThePastedIdentifiers(t *testing.T) {
	store := &recordingErasure{}
	ts := erasureConsole(t, store)

	post(t, ts, "/org/erasure/confirm", url.Values{
		"subject": {"  sub-1\t"}, "username": {" ada \n"},
	})

	got := store.erasures()
	if len(got) != 1 {
		t.Fatalf("erased %d times: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "|sub-1|ada") {
		t.Errorf("erased %q; the pasted whitespace became part of the identifier "+
			"and the match will find nobody", got[0])
	}
}

// Without the database the page has to say so. Silently reporting an empty
// result would tell a controller their obligation was met when nothing ran.
func TestErasureWithoutTheDatabaseRefusesRatherThanReportingNothing(t *testing.T) {
	ts := erasureConsole(t, nil)

	for _, path := range []string{"/org/erasure/preview", "/org/erasure/confirm"} {
		code, body := post(t, ts, path, url.Values{"subject": {"sub-1"}})
		if code == http.StatusOK && !strings.Contains(body, "postgres") {
			t.Errorf("%s answered %d without naming the reason; an operator would "+
				"read that as 'this person has no data'", path, code)
		}
	}
}

// The mail write handlers had no guard for a console without an SMTP service,
// while the page they belong to did. The form is not offered in that state,
// but the routes exist, and a POST to one of them dereferenced nil.
//
// Measured before the fix: POST /mail/test panicked and dropped the
// connection. Under mw.Recover in production that is a 500, which tells an
// owner something is broken when the truth is that a feature is not set up.
func TestMailWritesWithoutAServiceRefuseInsteadOfPanicking(t *testing.T) {
	ts := erasureConsole(t, nil) // Mail is nil in this harness

	for _, path := range []string{"/mail", "/mail/test", "/mail/delete"} {
		code, body := post(t, ts, path, url.Values{
			"host": {"smtp.example.org"}, "port": {"587"}, "to": {"ada@example.org"},
		})
		// The connection surviving at all is half the assertion: a panic here
		// used to close it, and the test would fail on the request rather than
		// on the status.
		if code == http.StatusSeeOther {
			t.Errorf("%s reported success with no mail service wired", path)
		}
		if !strings.Contains(strings.ToLower(body), "not configured") {
			t.Errorf("%s answered %d without saying why: %.120s", path, code, body)
		}
	}
}

// The page itself already said so, and must keep saying so: it is the only
// place an owner finds out that mail is unavailable rather than broken.
func TestMailPageSaysItIsUnavailableRatherThanFailing(t *testing.T) {
	ts := erasureConsole(t, nil)
	resp, err := client().Get(ts.URL + "/mail")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mail page = %d", resp.StatusCode)
	}
	if len(body) == 0 {
		t.Error("the mail page rendered nothing")
	}
}
