package api

import (
	"testing"
)

func TestIntentArmClearAuthz(t *testing.T) {
	srv := newTestAPI(t, true) // break-glass token = owner everywhere

	// Lock arms; wipe without lock refused; forced wipe ok.
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/intent",
		map[string]any{"intent": "lock"}, testToken); code != 200 {
		t.Fatalf("lock = %d", code)
	}
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/intent",
		map[string]any{"intent": "wipe", "force": true}, testToken); code != 200 {
		t.Fatalf("forced wipe = %d", code)
	}
	// Clear.
	if code, _ := call(t, srv, "DELETE", "/api/v1/devices/lt-1/intent", nil, testToken); code != 200 {
		t.Fatalf("clear = %d", code)
	}
	// Wipe without lock now refused (422 gate/domain).
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/intent",
		map[string]any{"intent": "wipe"}, testToken); code == 200 {
		t.Fatal("wipe without lock accepted")
	}
	// Bogus intent refused.
	if code, _ := call(t, srv, "POST", "/api/v1/devices/lt-1/intent",
		map[string]any{"intent": "explode"}, testToken); code == 200 {
		t.Fatal("bogus intent accepted")
	}
}
