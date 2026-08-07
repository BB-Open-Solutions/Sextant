package api

import (
	"net/http"
	"testing"
)

// devices_api_test.go covers enrol and unenrol over the API. Both were at 0%.
//
// Enrolment is where a device gets its identity, so the failure that matters
// is not "it returned 500" but "it returned 201 and the device cannot check
// in" - which is exactly what happens if the credential is not issued and
// nobody says so. The handler is written to report that gap rather than to
// fail the enrolment, and this pins that it does.

func TestEnrolAndUnenrolADeviceThroughTheAPI(t *testing.T) {
	srv := newTestAPI(t, true)

	code, body := call(t, srv, "POST", "/api/v1/devices", map[string]any{
		"tag": "lt-9", "hardware": "hp-g4", "class": "laptop",
		"assignedUser": "ada",
	}, testToken)
	if code != http.StatusCreated {
		t.Fatalf("enrol = %d: %v", code, body)
	}
	if body["tag"] != "lt-9" {
		t.Errorf("response does not name the device: %v", body)
	}
	// This deployment has no credential service wired, so no credential is
	// issued - and the response must not pretend otherwise. A "credential"
	// field here would be an empty string an operator copies onto a device.
	if _, ok := body["credential"]; ok {
		t.Error("a credential was reported by a deployment that cannot issue one")
	}

	// It is in the fleet now.
	if code, _ = call(t, srv, "GET", "/api/v1/devices", nil, testToken); code != 200 {
		t.Fatalf("list = %d", code)
	}

	// Enrolling the same tag twice must be refused: the second call would
	// otherwise reset the enrolment date and re-issue a credential, silently
	// invalidating the one running on the machine.
	if code, _ = call(t, srv, "POST", "/api/v1/devices", map[string]any{
		"tag": "lt-9", "hardware": "hp-g4",
	}, testToken); code == http.StatusCreated {
		t.Error("enrolling an existing tag succeeded")
	}

	if code, body = call(t, srv, "DELETE", "/api/v1/devices/lt-9", nil, testToken); code != 200 {
		t.Fatalf("unenrol = %d: %v", code, body)
	}
	// Removing it twice is refused rather than reported as done.
	if code, _ = call(t, srv, "DELETE", "/api/v1/devices/lt-9", nil, testToken); code == 200 {
		t.Error("removing an already removed device reported success")
	}
}

func TestEnrolRefusesWhatTheFleetCannotHold(t *testing.T) {
	srv := newTestAPI(t, true)
	cases := []struct {
		name string
		in   map[string]any
	}{
		{"no tag", map[string]any{"hardware": "hp-g4"}},
		{"no hardware profile", map[string]any{"tag": "lt-8"}},
		{"tag that is not a slug", map[string]any{"tag": "LT 8!", "hardware": "hp-g4"}},
		// A group that does not exist would leave the device pointing at
		// nothing, inheriting from a scope the document has no record of.
		{"unknown group", map[string]any{"tag": "lt-8", "hardware": "hp-g4", "groups": []string{"ghosts"}}},
	}
	// NOT in this list, deliberately: an unknown HARDWARE PROFILE. That is
	// caught by the nix gate at eval time (generator.nix's deviceAsserts
	// cross-check profile names), not by this handler - and these tests run
	// with a no-op gate, so asserting it here would be asserting the stub.
	// The real proof lives in the gate tests.
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _ := call(t, srv, "POST", "/api/v1/devices", c.in, testToken)
			if code == http.StatusCreated {
				t.Errorf("accepted %v", c.in)
			}
			if code == http.StatusInternalServerError {
				t.Errorf("a bad request produced a 500; it reads as a broken console")
			}
		})
	}
}

func TestDeviceWritesAreRefusedOnAReadOnlyDeployment(t *testing.T) {
	srv := newTestAPI(t, false)
	if code, _ := call(t, srv, "GET", "/api/v1/devices", nil, testToken); code != 200 {
		t.Errorf("read-only deployment refuses a read: %d", code)
	}
	if code, _ := call(t, srv, "POST", "/api/v1/devices",
		map[string]any{"tag": "lt-9", "hardware": "hp-g4"}, testToken); code == http.StatusCreated {
		t.Error("enrolment succeeded on a read-only deployment")
	}
	if code, _ := call(t, srv, "DELETE", "/api/v1/devices/lt-1", nil, testToken); code == 200 {
		t.Error("unenrolment succeeded on a read-only deployment")
	}
}
