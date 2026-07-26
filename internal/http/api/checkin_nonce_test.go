package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

var nonceKey = []byte("0123456789abcdef0123456789abcdef")

// TestFleetIntentWipeConstMatches pins the local const equal to the domain's,
// so the device-facing API decoupling never silently drifts.
func TestFleetIntentWipeConstMatches(t *testing.T) {
	if fleetIntentWipe != fleet.IntentWipe {
		t.Fatalf("fleetIntentWipe %q != fleet.IntentWipe %q", fleetIntentWipe, fleet.IntentWipe)
	}
}

func TestIntentNonceSignVerify(t *testing.T) {
	now := int64(1_700_000_000)
	nonce := signIntentNonce(nonceKey, "t495s", "wipe", now)
	if !verifyIntentNonce(nonceKey, "t495s", "wipe", now, now, nonce) {
		t.Fatal("valid nonce rejected")
	}
	// Fresh within the window.
	if !verifyIntentNonce(nonceKey, "t495s", "wipe", now, now+intentNonceWindow-1, nonce) {
		t.Fatal("nonce within window rejected")
	}
	// Stale beyond the window is a replay.
	if verifyIntentNonce(nonceKey, "t495s", "wipe", now, now+intentNonceWindow+1, nonce) {
		t.Fatal("stale nonce accepted")
	}
	// Wrong tag / intent / key / a future ts all fail.
	if verifyIntentNonce(nonceKey, "other", "wipe", now, now, nonce) {
		t.Fatal("wrong tag accepted")
	}
	if verifyIntentNonce(nonceKey, "t495s", "lock", now, now, nonce) {
		t.Fatal("wrong intent accepted")
	}
	if verifyIntentNonce([]byte("different-key-different-key-xxxx"), "t495s", "wipe", now, now, nonce) {
		t.Fatal("wrong key accepted")
	}
	if verifyIntentNonce(nonceKey, "t495s", "wipe", now+5, now, nonce) {
		t.Fatal("future ts accepted")
	}
	if verifyIntentNonce(nil, "t495s", "wipe", now, now, nonce) {
		t.Fatal("no key accepted")
	}
}

func nonceServer(t *testing.T, fo *fakeObserved, intent string, now time.Time) *httptest.Server {
	t.Helper()
	inv := app.NewInventoryService(fo, fo, fixedClock{now}, "")
	mux := http.NewServeMux()
	NewCheckin(inv, nil, "bridge-tok").
		WithIntent(func(_ context.Context, _ string) string { return intent }).
		WithIntentKey(nonceKey).
		WithClock(func() time.Time { return now }).
		Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer bridge-tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var doc map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &doc)
	}
	return resp.StatusCode, doc
}

// TestWipeIntentCarriesSignedNonce: a wipe intent rides back with a nonce+ts
// that verifies; a non-destructive intent (lock) carries none.
func TestWipeIntentCarriesSignedNonce(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fo := newFakeObserved()
	srv := nonceServer(t, fo, "wipe", now)
	code, doc := postJSON(t, srv.URL+"/api/checkin", `{"tag":"t495s","revision":"v1"}`)
	if code != 200 {
		t.Fatalf("wipe intent = %d", code)
	}
	nonce, _ := doc["nonce"].(string)
	ts, _ := doc["ts"].(float64)
	if nonce == "" || ts == 0 {
		t.Fatalf("wipe response missing nonce/ts: %v", doc)
	}
	if !verifyIntentNonce(nonceKey, "t495s", "wipe", int64(ts), now.Unix(), nonce) {
		t.Fatal("issued nonce does not verify")
	}

	// A lock intent carries no nonce (only the destructive wipe is signed).
	fo2 := newFakeObserved()
	srv2 := nonceServer(t, fo2, "lock", now)
	_, ld := postJSON(t, srv2.URL+"/api/checkin", `{"tag":"t495s","revision":"v1"}`)
	if _, has := ld["nonce"]; has {
		t.Fatalf("lock intent should not carry a nonce: %v", ld)
	}
}

// TestWipeAckVerification: a wipe ack with a valid nonce is recorded; a forged
// or stale one is dropped so it cannot pass as a real destructive result.
func TestWipeAckVerification(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	goodNonce := signIntentNonce(nonceKey, "t495s", "wipe", now.Unix())

	// Valid ack: stored.
	fo := newFakeObserved()
	srv := nonceServer(t, fo, "", now)
	body := `{"tag":"t495s","revision":"v1","ack":"wipe","ackNonce":"` + goodNonce + `","ackTs":1700000000}`
	if code, _ := postJSON(t, srv.URL+"/api/checkin", body); code != 204 {
		t.Fatalf("valid wipe ack = %d", code)
	}
	if st, _, _ := fo.Get(context.Background(), app.DefaultTenant, "t495s"); st.Ack != observed.AckWipe {
		t.Fatalf("valid wipe ack not recorded: %q", st.Ack)
	}

	// Forged ack (no/blank nonce): dropped.
	fo2 := newFakeObserved()
	srv2 := nonceServer(t, fo2, "", now)
	if code, _ := postJSON(t, srv2.URL+"/api/checkin",
		`{"tag":"t495s","revision":"v1","ack":"wipe"}`); code != 204 {
		t.Fatalf("forged wipe ack = %d", code)
	}
	if st, _, _ := fo2.Get(context.Background(), app.DefaultTenant, "t495s"); st.Ack == observed.AckWipe {
		t.Fatal("forged wipe ack was recorded")
	}

	// Stale ack (nonce valid but > window old): dropped.
	fo3 := newFakeObserved()
	late := now.Add((intentNonceWindow + 60) * time.Second)
	srv3 := nonceServer(t, fo3, "", late)
	if code, _ := postJSON(t, srv3.URL+"/api/checkin", body); code != 204 {
		t.Fatalf("stale wipe ack = %d", code)
	}
	if st, _, _ := fo3.Get(context.Background(), app.DefaultTenant, "t495s"); st.Ack == observed.AckWipe {
		t.Fatal("stale wipe ack was recorded")
	}
}
