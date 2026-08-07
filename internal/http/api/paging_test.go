package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// paging_test.go covers audit finding A2. The property that matters most is
// the FIRST test: a caller that asks for nothing must see exactly what it saw
// before paging existed. Everything else here is a feature; that one is the
// promise that let this ship before the 1.0 freeze.

// fleetWithDevices returns a server whose fleet holds n devices.
func fleetWithDevices(t *testing.T, n int) *httptest.Server {
	t.Helper()
	srv := newTestAPI(t, true)
	for i := 0; i < n; i++ {
		code, body := call(t, srv, "POST", "/api/v1/devices", map[string]any{
			"tag": "lt-p" + strconv.Itoa(i), "hardware": "hp-g4", "class": "laptop",
		}, testToken)
		if code != http.StatusCreated {
			t.Fatalf("seed device %d = %d: %v", i, code, body)
		}
	}
	return srv
}

// listDevices returns the decoded array and the X-Total-Count header.
func listDevices(t *testing.T, srv *httptest.Server, query string) ([]any, string, int) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/devices"+query, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []any
	if resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("not a JSON array: %v", err)
		}
	}
	return out, resp.Header.Get("X-Total-Count"), resp.StatusCode
}

// TestAskingForNothingChangesNothing is the compatibility promise. A client
// written before paging existed must be byte-for-byte unaffected: a bare
// array, all of it, no wrapper.
func TestAskingForNothingChangesNothing(t *testing.T) {
	srv := fleetWithDevices(t, 5)
	got, total, code := listDevices(t, srv, "")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// The seed already carries one device, so five more makes six.
	if len(got) != 6 {
		t.Errorf("got %d devices with no paging asked for, want all 6", len(got))
	}
	if total != "6" {
		t.Errorf("X-Total-Count = %q, want 6", total)
	}
}

func TestLimitAndOffsetWalkTheList(t *testing.T) {
	srv := fleetWithDevices(t, 5)

	first, total, _ := listDevices(t, srv, "?limit=2")
	if len(first) != 2 {
		t.Fatalf("limit=2 returned %d", len(first))
	}
	// The total is the UNPAGED size, so a client can tell how far it has to
	// walk without walking it.
	if total != "6" {
		t.Errorf("X-Total-Count = %q on a page, want the unpaged 6", total)
	}

	second, _, _ := listDevices(t, srv, "?limit=2&offset=2")
	if len(second) != 2 {
		t.Fatalf("second page returned %d", len(second))
	}
	// Pages must not overlap, or a caller walking the list sees a device
	// twice and misses another.
	if first[0].(map[string]any)["tag"] == second[0].(map[string]any)["tag"] {
		t.Error("page two repeats page one")
	}

	// Walking to the end and past it.
	last, _, _ := listDevices(t, srv, "?offset=4")
	if len(last) != 2 {
		t.Errorf("offset=4 returned %d, want the remaining 2", len(last))
	}
	past, totalPast, code := listDevices(t, srv, "?offset=99")
	if code != 200 {
		t.Errorf("offset past the end = %d, want 200", code)
	}
	if len(past) != 0 {
		t.Errorf("offset past the end returned %d items", len(past))
	}
	// Empty, not null: paging off the end is the empty case, and the same
	// rule applies to it (see A5).
	if totalPast != "6" {
		t.Errorf("X-Total-Count past the end = %q, want 6", totalPast)
	}
}

// TestAMalformedPageIsRefused. Ignoring a bad parameter would serve the
// whole list to a caller who asked for ten and believes they got ten - and
// they would page through it forever.
func TestAMalformedPageIsRefused(t *testing.T) {
	srv := newTestAPI(t, true)
	for _, q := range []string{
		"?limit=lots", "?limit=-1", "?offset=-1", "?offset=soon",
		"?limit=1000000", // over the per-request cap
	} {
		t.Run(q, func(t *testing.T) {
			_, _, code := listDevices(t, srv, q)
			if code == 200 {
				t.Errorf("accepted %s; the caller would page forever", q)
			}
			if code == http.StatusInternalServerError {
				t.Errorf("%s produced a 500", q)
			}
		})
	}
}

// TestPagingIsScopedToWhatTheCallerMaySee: the total must count the
// caller's visible list, not the fleet. Otherwise X-Total-Count leaks the
// size of what they are not allowed to see.
func TestPagingIsScopedToWhatTheCallerMaySee(t *testing.T) {
	srv := fleetWithDevices(t, 3)
	got, total, _ := listDevices(t, srv, "")
	if total != strconv.Itoa(len(got)) {
		t.Errorf("X-Total-Count = %q but the caller sees %d; the total is not their list",
			total, len(got))
	}
}
