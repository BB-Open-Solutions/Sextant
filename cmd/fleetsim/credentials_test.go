package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The whole point is the handover between the two simulators: the station
// receives a credential at claim time and the beat has to use it. Testing the
// store on its own would pass with the wiring cut, which is the failure this
// test exists to prevent, so it drives claim() and beatOne() against a
// console that records what it was sent.
func TestADeviceImagedByTheStationChecksInAsItself(t *testing.T) {
	const (
		bridge = "shared-bridge-token"
		issued = "credential-only-this-device-has"
		tag    = "lt-imaged-1"
		mac    = "aa:bb:cc:dd:ee:01"
	)

	var mu sync.Mutex
	var checkinAuth []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/jobs/claim"):
			_ = json.NewEncoder(w).Encode([]map[string]string{{
				"mac": mac, "tag": tag, "hardware": "lenovo-t495s",
				"status": "imaging", "credential": issued,
			}})
		case strings.HasSuffix(r.URL.Path, "/api/checkin"):
			mu.Lock()
			checkinAuth = append(checkinAuth, r.Header.Get("Authorization"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}
	}))
	defer srv.Close()

	creds := newCredStore()
	sta := newStationSim(srv.URL, bridge, "st-1", 1, 0, false, creds)
	sim := &simulator{
		url:    srv.URL,
		token:  bridge,
		client: &http.Client{Timeout: 5 * time.Second},
		state:  map[string]*devState{},
		creds:  creds,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Before imaging there is no credential, so the bridge token is correct.
	sim.beatOne(ctx, device{Tag: tag, Group: "kantoor-a"}, map[string]string{})

	// The station needs a machine on its network to claim the job onto.
	sta.mu.Lock()
	sta.machines[mac] = &machine{MAC: mac}
	sta.mu.Unlock()
	sta.claim(ctx)

	sim.beatOne(ctx, device{Tag: tag, Group: "kantoor-a"}, map[string]string{})

	mu.Lock()
	defer mu.Unlock()
	if len(checkinAuth) != 2 {
		t.Fatalf("expected two check-ins, got %d", len(checkinAuth))
	}
	if checkinAuth[0] != "Bearer "+bridge {
		t.Errorf("before imaging the device sent %q; a device with no credential of "+
			"its own should use the bridge token", checkinAuth[0])
	}
	if checkinAuth[1] != "Bearer "+issued {
		t.Errorf("after imaging the device sent %q, want the credential it was issued; "+
			"the console refuses the bridge token for a device that has one, so this "+
			"device would be stuck at never seen", checkinAuth[1])
	}
}

// A device the station never touched must keep using the bridge token. The
// obvious wrong fix - send the last credential seen to everyone - would leave
// this passing only by accident, so it is asserted rather than assumed.
func TestADeviceTheStationNeverTouchedStillUsesTheBridgeToken(t *testing.T) {
	c := newCredStore()
	c.put("lt-1", "secret-for-lt-1")

	if got := c.get("lt-2"); got != "" {
		t.Errorf("lt-2 was handed %q, which belongs to another device", got)
	}
	if got := c.get("lt-1"); got != "secret-for-lt-1" {
		t.Errorf("lt-1 got %q", got)
	}
}
