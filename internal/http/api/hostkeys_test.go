package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// hkSeed: two sibling groups. a-1 and b-1 have a host key on file, a-2 has
// none, a-3 has one but is retired.
const hkSeed = `{
  "version": 3,
  "groups": {"alpha": {}, "beta": {}},
  "devices": {
    "a-1": {"groups": ["alpha"], "hardware": "hw", "itam": {"hostKeyId": "` + testHostKey + `"}},
    "a-2": {"groups": ["alpha"], "hardware": "hw"},
    "a-3": {"groups": ["alpha"], "hardware": "hw", "state": "retired", "itam": {"hostKeyId": "` + testHostKeyB + `"}},
    "b-1": {"groups": ["beta"], "hardware": "hw", "itam": {"hostKeyId": "` + testHostKeyB + `"}}
  },
  "access": [
    {"group": "alpha-team", "role": "viewer", "scope": "group:alpha"},
    {"group": "org-team", "role": "viewer", "scope": "org"}
  ]
}`

func newHostKeysAPI(t *testing.T, u identity.User) *httptest.Server {
	t.Helper()
	svc, _ := seededService(t, hkSeed)
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{Sessions: visSessions{u: u}}, "", false, discardLog()).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func getHostKeyList(t *testing.T, srv *httptest.Server) (int, []hostKeyEntry) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/hostkeys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out []hostKeyEntry
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestHostKeysListsActiveDevicesWithKeys(t *testing.T) {
	srv := newHostKeysAPI(t, identity.User{Subject: "org", Groups: []string{"org-team"}})
	code, out := getHostKeyList(t, srv)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	got := map[string]string{}
	for _, e := range out {
		got[e.Tag] = e.HostKey
	}
	// a-2 has no key to encrypt for, a-3 is retired: neither is a recipient.
	if len(got) != 2 || got["a-1"] != testHostKey || got["b-1"] != testHostKeyB {
		t.Fatalf("host keys = %v, want a-1 and b-1 only", got)
	}
}

// Read-confidentiality: a group-scoped viewer never learns a foreign tag,
// host keys included.
func TestHostKeysRespectVisibility(t *testing.T) {
	srv := newHostKeysAPI(t, identity.User{Subject: "alpha", Groups: []string{"alpha-team"}})
	code, out := getHostKeyList(t, srv)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(out) != 1 || out[0].Tag != "a-1" {
		t.Fatalf("scoped viewer sees %v, want a-1 only", out)
	}
}

// No role anywhere is no read: the wrapper's viewer floor covers this
// endpoint like every other one.
func TestHostKeysRequireARole(t *testing.T) {
	srv := newHostKeysAPI(t, identity.User{Subject: "nobody", Groups: []string{"unrelated"}})
	if code, _ := getHostKeyList(t, srv); code != http.StatusForbidden {
		t.Fatalf("roleless read = %d, want 403", code)
	}
}
