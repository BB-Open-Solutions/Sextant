package main

import (
	"net/http"
	"testing"
	"time"
)

// A write may have to wait for the Nix gate: one evaluation per configuration
// shape the edit touches, and every shape when something invalidates the
// verdict memo. Bounding that the same as a read reported "context deadline
// exceeded" for an edit the server applied successfully - measured 2026-08-04,
// setting localAdmin.passwordSecret after a rekey.
//
// A write that looks failed and is not is the worst answer available, because
// the obvious response is to run it again.
func TestWritesGetLongerThanReads(t *testing.T) {
	if got := timeoutFor(http.MethodGet); got != readTimeout {
		t.Fatalf("GET timeout = %v, want %v", got, readTimeout)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if got := timeoutFor(m); got != writeTimeout {
			t.Fatalf("%s timeout = %v, want %v", m, got, writeTimeout)
		}
	}
	// The bound has to clear a realistic cold validation, not just the single
	// shape that happened to be measured. Eight seconds a shape, and a fleet
	// has more than four.
	if writeTimeout < 2*time.Minute {
		t.Fatalf("writeTimeout %v is too tight for a cold validation of a real fleet", writeTimeout)
	}
}
