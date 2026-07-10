package api

import (
	"testing"
)

// TestFleetOpsEndToEnd drives the new management operations through the
// full API stack (break-glass token, real temp git repo, allow-all gate).
func TestFleetOpsEndToEnd(t *testing.T) {
	srv := newTestAPI(t, true)

	// Device update: class + labels.
	code, _ := call(t, srv, "PATCH", "/api/v1/devices/lt-1",
		map[string]any{"class": "kiosk", "labels": map[string]string{"site": "hq"}}, testToken)
	if code != 200 {
		t.Fatalf("patch device = %d", code)
	}
	// Unknown device 4xx, not 500.
	if code, _ := call(t, srv, "PATCH", "/api/v1/devices/ghost",
		map[string]any{"class": "x"}, testToken); code != 422 && code != 400 && code != 404 {
		t.Fatalf("patch ghost = %d", code)
	}

	// Group management: add, re-parent, remove.
	if code, _ := call(t, srv, "POST", "/api/v1/groups",
		map[string]any{"name": "front", "parent": "pilot"}, testToken); code != 201 {
		t.Fatalf("add group = %d", code)
	}
	if code, _ := call(t, srv, "PATCH", "/api/v1/groups/front",
		map[string]any{"parent": ""}, testToken); code != 200 {
		t.Fatalf("patch group = %d", code)
	}
	if code, _ := call(t, srv, "DELETE", "/api/v1/groups/front", nil, testToken); code != 200 {
		t.Fatalf("delete group = %d", code)
	}
	// Removing a referenced group is refused.
	if code, _ := call(t, srv, "DELETE", "/api/v1/groups/pilot", nil, testToken); code == 200 {
		t.Fatal("removed group with devices")
	}

	// Apps: replace list, firewall enforced.
	if code, _ := call(t, srv, "PUT", "/api/v1/apps",
		map[string]any{"scope": "group:pilot", "kind": "packages", "names": []string{"vlc"}}, testToken); code != 200 {
		t.Fatalf("put apps = %d", code)
	}
	if code, _ := call(t, srv, "PUT", "/api/v1/apps",
		map[string]any{"scope": "org", "kind": "packages", "names": []string{"bad name"}}, testToken); code == 200 {
		t.Fatal("injection name accepted")
	}

	// Rollout plan + assurance.
	if code, _ := call(t, srv, "PUT", "/api/v1/rollout/plan",
		map[string]any{"plan": map[string]any{"rings": []map[string]any{{"group": "pilot"}}}}, testToken); code != 200 {
		t.Fatalf("put plan = %d", code)
	}
	if code, _ := call(t, srv, "PUT", "/api/v1/assurance",
		map[string]any{"requireFourEyes": true}, testToken); code != 200 {
		t.Fatalf("put assurance = %d", code)
	}

	// Lifecycle: retire, reads reflect it, reactivate.
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/retire", nil, testToken); code != 200 {
		t.Fatalf("retire = %d", code)
	}
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/retire", nil, testToken); code == 200 {
		t.Fatal("double retire accepted")
	}
	if _, body := call(t, srv, "GET", "/api/v1/devices/lt-1", nil, testToken); body["device"].(map[string]any)["state"] != "retired" {
		t.Fatalf("device not retired in read: %v", body["device"])
	}
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/reactivate", nil, testToken); code != 200 {
		t.Fatalf("reactivate = %d", code)
	}
}
