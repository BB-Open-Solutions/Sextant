package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
)

// station_ops_test.go covers the station write handlers, which were at 0%:
// register, remove, and mint a report credential. All three are org Owner,
// and that is the property worth pinning - a station credential lets a host
// push discoveries into the fleet's register, so it is not an Editor's call.

func TestRegisterAndRemoveAStation(t *testing.T) {
	ts := newStationListConsole(t, &memDisc{sets: map[string][]discovery.Discovered{}}, nil)
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	resp := post("/stations", url.Values{
		"tag": {"nuc-3"}, "description": {"third bench"}, "site": {"kelder"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("register = %d", resp.StatusCode)
	}
	// It has to actually show up: a 303 onto a page that does not list it
	// is the failure an operator reports as "it did nothing".
	if body := getBody(t, ts.URL+"/station"); !strings.Contains(body, "nuc-3") {
		t.Error("the new station does not appear on the station page")
	}

	// An empty tag is refused rather than registering a nameless station.
	if resp := post("/stations", url.Values{"description": {"x"}}); resp.StatusCode == 303 {
		t.Error("a station with no tag was registered")
	}

	if resp := post("/station/nuc-3/remove", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("remove = %d", resp.StatusCode)
	}
	if body := getBody(t, ts.URL+"/station"); strings.Contains(body, "third bench") {
		t.Error("the station is still listed after removal")
	}
	// Removing it twice is refused, not reported as done.
	if resp := post("/station/nuc-3/remove", url.Values{}); resp.StatusCode == 303 {
		t.Error("removing an already removed station reported success")
	}
}

// TestMintingAStationCredentialNeedsTheTokenStore: without Postgres there is
// nowhere to put a credential, and the handler must say so rather than
// redirect to a page that shows nothing.
func TestMintingAStationCredentialNeedsTheTokenStore(t *testing.T) {
	ts := newStationListConsole(t, &memDisc{sets: map[string][]discovery.Discovered{}}, nil)
	form := url.Values{"csrf": {"dev-csrf"}}
	resp, err := client().PostForm(ts.URL+"/station/nuc-1/credential", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Error("minting succeeded with no token store; the page would show an empty secret")
	}
}
