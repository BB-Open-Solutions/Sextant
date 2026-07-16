package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRunDispatchSmoke sweeps the remaining subcommands - the thin CRUD
// wrappers in commands.go/commands_shipping.go and the inline fleet/me/
// audit/evidence branches in dispatch - that the detailed happy-path/401/
// Bearer tests in client_test.go don't already exercise. Each case is just
// "does sxctl hit the right HTTP method+path and exit 0", not a full
// behavioral test: the request-shaping details (body construction, output
// formatting) are covered narrowly and thoroughly elsewhere. This is what
// turns a package that had zero tests into one where every dispatch branch
// is at least known to be wired correctly.
func TestRunDispatchSmoke(t *testing.T) {
	const listResp = `[]`
	const objResp = `{}`
	const statusResp = `{"status":"ok"}`
	const textResp = `some diff text`

	cases := []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
		resp       string
	}{
		{"devices get", []string{"devices", "get", "nuc-01"}, http.MethodGet, "/api/v1/devices/nuc-01", objResp},
		{"devices enroll", []string{"devices", "enroll", "nuc-02", "-hardware", "nuc11"}, http.MethodPost, "/api/v1/devices", objResp},
		{"devices update", []string{"devices", "update", "nuc-01", "-class", "station"}, http.MethodPatch, "/api/v1/devices/nuc-01", objResp},
		{"devices retire", []string{"devices", "retire", "nuc-01"}, http.MethodPost, "/api/v1/devices/nuc-01/retire", objResp},
		{"devices reactivate", []string{"devices", "reactivate", "nuc-01"}, http.MethodPost, "/api/v1/devices/nuc-01/reactivate", objResp},
		{"devices remove", []string{"devices", "remove", "nuc-01"}, http.MethodDelete, "/api/v1/devices/nuc-01", objResp},
		{"devices lock", []string{"devices", "lock", "nuc-01"}, http.MethodPost, "/api/v1/devices/nuc-01/intent", objResp},
		{"devices wipe", []string{"devices", "wipe", "nuc-01", "-force"}, http.MethodPost, "/api/v1/devices/nuc-01/intent", objResp},
		{"devices unlock", []string{"devices", "unlock", "nuc-01"}, http.MethodDelete, "/api/v1/devices/nuc-01/intent", objResp},

		{"groups add", []string{"groups", "add", "engineering", "-parent", "org"}, http.MethodPost, "/api/v1/groups", objResp},
		{"groups update", []string{"groups", "update", "engineering", "-idp", "eng-idp"}, http.MethodPatch, "/api/v1/groups/engineering", objResp},
		{"groups remove", []string{"groups", "remove", "engineering"}, http.MethodDelete, "/api/v1/groups/engineering", objResp},

		{"apps set", []string{"apps", "set", "org", "packages", "vim", "git"}, http.MethodPut, "/api/v1/apps", objResp},

		{"settings set", []string{"settings", "set", "org", "theme", "dark"}, http.MethodPost, "/api/v1/settings", objResp},
		{"settings clear", []string{"settings", "clear", "org", "theme"}, http.MethodDelete, "/api/v1/settings", objResp},

		{"changes list", []string{"changes", "list"}, http.MethodGet, "/api/v1/changes", listResp},
		{"changes open", []string{"changes", "open", "cr-1", "add theme"}, http.MethodPost, "/api/v1/changes", objResp},
		{"changes edit", []string{"changes", "edit", "cr-1", "org", "theme", "dark"}, http.MethodPost, "/api/v1/changes/cr-1/edits", objResp},
		{"changes diff", []string{"changes", "diff", "cr-1"}, http.MethodGet, "/api/v1/changes/cr-1/diff", textResp},
		{"changes submit", []string{"changes", "submit", "cr-1"}, http.MethodPost, "/api/v1/changes/cr-1/submit", statusResp},
		{"changes merge", []string{"changes", "merge", "cr-1"}, http.MethodPost, "/api/v1/changes/cr-1/merge", statusResp},
		{"changes abandon", []string{"changes", "abandon", "cr-1"}, http.MethodPost, "/api/v1/changes/cr-1/abandon", statusResp},

		{"rollout start", []string{"rollout", "start", "v1.2.3"}, http.MethodPost, "/api/v1/rollout", objResp},
		{"rollout tick", []string{"rollout", "tick"}, http.MethodPost, "/api/v1/rollout/tick", objResp},
		{"rollout cancel", []string{"rollout", "cancel"}, http.MethodDelete, "/api/v1/rollout", objResp},

		{"status all", []string{"status"}, http.MethodGet, "/api/v1/status", listResp},
		{"status one", []string{"status", "nuc-01"}, http.MethodGet, "/api/v1/status/nuc-01", objResp},

		{"access list", []string{"access", "list"}, http.MethodGet, "/api/v1/access", listResp},
		{"access grant", []string{"access", "grant", "eng-idp", "editor", "org"}, http.MethodPost, "/api/v1/access", objResp},
		{"access revoke", []string{"access", "revoke", "eng-idp", "org"}, http.MethodDelete, "/api/v1/access", objResp},

		{"tokens list", []string{"tokens", "list"}, http.MethodGet, "/api/v1/tokens", listResp},
		{"tokens revoke", []string{"tokens", "revoke", "tok-1"}, http.MethodDelete, "/api/v1/tokens/tok-1", objResp},

		{"me", []string{"me"}, http.MethodGet, "/api/v1/me", objResp},
		{"me prefs get", []string{"me", "prefs"}, http.MethodGet, "/api/v1/me/preferences", objResp},
		{"me prefs set", []string{"me", "prefs", "Europe/Amsterdam", "nl"}, http.MethodPut, "/api/v1/me/preferences", objResp},

		{"audit", []string{"audit"}, http.MethodGet, "/api/v1/audit", objResp},
		{"evidence default", []string{"evidence"}, http.MethodGet, "/api/v1/evidence", objResp},
		// NOTE: this documents an existing dispatch quirk, not the intent in
		// `usage`. dispatch()'s generic res/verb/rest split treats args[1] as
		// "verb" for every resource, including evidence, which has no verb -
		// just two positional dates. So "evidence FROM TO" (3 args) leaves
		// FROM stuck in the discarded verb variable and rest with only TO,
		// evidence has no verb: its two positional dates build the range
		// query (this was dead code until the audit fix - the generic verb
		// split swallowed FROM).
		{"evidence with two positional dates builds the range query",
			[]string{"evidence", "2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z"},
			http.MethodGet, "/api/v1/evidence?from=2026-01-01T00%3A00%3A00Z&to=2026-02-01T00%3A00%3A00Z", objResp},

		{"fleet", []string{"fleet"}, http.MethodGet, "/api/v1/fleet", objResp},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.RequestURI()
				_, _ = io.Copy(io.Discard, r.Body) // drain so the client's write side never blocks
				_, _ = w.Write([]byte(tc.resp))
			}))
			defer srv.Close()

			code, out, errOut := runAgainst(t, srv, tc.args...)

			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stdout=%q stderr=%q", code, out, errOut)
			}
			if gotMethod != tc.wantMethod {
				t.Errorf("method = %s, want %s", gotMethod, tc.wantMethod)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %s, want %s", gotPath, tc.wantPath)
			}
		})
	}
}
