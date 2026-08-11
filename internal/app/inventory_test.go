package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// memStatus is an in-memory ports.StatusStore for check-in tests.
type memStatus struct {
	m map[string]observed.DeviceStatus
}

func newMemStatus() *memStatus { return &memStatus{m: map[string]observed.DeviceStatus{}} }

// Upsert mirrors the real store's contract: ackChanged reports whether the
// ack differs from what was stored immediately before this write, computed
// as part of the same call (no separate prior read by the caller).
func (s *memStatus) Upsert(_ context.Context, _ string, c observed.CheckIn, _ time.Time) (bool, error) {
	prev, existed := s.m[c.Tag]
	ackChanged := !existed || prev.Ack != c.Ack
	s.m[c.Tag] = observed.DeviceStatus{Tag: c.Tag, Ack: c.Ack, SB: c.SB, TPM2: c.TPM2}
	return ackChanged, nil
}
func (s *memStatus) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	st, ok := s.m[tag]
	return st, ok, nil
}
func (s *memStatus) List(context.Context, string) ([]observed.DeviceStatus, error) { return nil, nil }
func (s *memStatus) Ping(context.Context) error                                    { return nil }

// nopFacts is an inert ports.InventoryStore.
type nopFacts struct{}

func (nopFacts) PutFacts(context.Context, string, string, []byte, time.Time) error { return nil }
func (nopFacts) GetFacts(context.Context, string, string) ([]byte, time.Time, bool, error) {
	return nil, time.Time{}, false, nil
}

func TestCheckInNotifiesWipeOncePerTransition(t *testing.T) {
	store := newMemStatus()
	notifyStore := newFakeNotifyStore()
	notifier := NewNotifyService(notifyStore, clockAt{time.Unix(1, 0)}, "default")
	inv := NewInventoryService(store, nopFacts{}, clockAt{time.Unix(1, 0)}, "default").
		WithNotifier(notifier, []string{"owners"})
	ctx := context.Background()

	beat := func(ack string) {
		if err := inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-01", Ack: ack}, nil); err != nil {
			t.Fatalf("checkin(%q): %v", ack, err)
		}
	}

	// A plain beat never notifies.
	beat("")
	// The first wipe ack notifies once...
	beat(observed.AckWipe)
	// ...and repeated echoes of the same ack do not re-notify.
	beat(observed.AckWipe)
	beat(observed.AckWipe)

	got := 0
	for _, n := range notifyStore.added {
		if n.Kind == notify.WipeExecuted {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("want exactly 1 wipe notification across repeated echoes, got %d", got)
	}
}

// TestCheckInRejectsBadFactsBeforeAnySideEffect (finding: CheckIn recorded
// status and fired the wipe notification before validating facts): a
// malformed facts payload must reject the whole check-in with no status
// write and no notification, not a partially-applied call.
func TestCheckInRejectsBadFactsBeforeAnySideEffect(t *testing.T) {
	store := newMemStatus()
	notifyStore := newFakeNotifyStore()
	notifier := NewNotifyService(notifyStore, clockAt{time.Unix(1, 0)}, "default")
	inv := NewInventoryService(store, nopFacts{}, clockAt{time.Unix(1, 0)}, "default").
		WithNotifier(notifier, []string{"owners"})
	ctx := context.Background()

	// Invalid JSON alongside a wipe ack: the facts error must win, and it must
	// win BEFORE the status upsert and the wipe notification happen.
	err := inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-01", Ack: observed.AckWipe}, []byte("{not json"))
	if err == nil {
		t.Fatal("want an error for invalid facts JSON")
	}
	if _, ok, _ := store.Get(ctx, "default", "nuc-01"); ok {
		t.Fatal("status must not be recorded when facts validation fails")
	}
	for _, n := range notifyStore.added {
		if n.Kind == notify.WipeExecuted {
			t.Fatal("wipe notification must not fire when facts validation fails")
		}
	}

	// Oversized facts: same guarantee.
	big := make([]byte, maxFactsBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-02", Ack: observed.AckWipe}, big); err == nil {
		t.Fatal("want an error for an oversized facts payload")
	}
	if _, ok, _ := store.Get(ctx, "default", "nuc-02"); ok {
		t.Fatal("status must not be recorded for an oversized facts payload")
	}
}

func TestCheckInNoNotifierIsInert(t *testing.T) {
	// Without a notifier (no Postgres), a wipe ack still records fine.
	inv := NewInventoryService(newMemStatus(), nopFacts{}, clockAt{time.Unix(1, 0)}, "default")
	if err := inv.CheckIn(context.Background(), observed.CheckIn{Tag: "x", Ack: observed.AckWipe}, nil); err != nil {
		t.Fatalf("checkin: %v", err)
	}
}

// recordingFacts captures what reached the store, so a test can assert on
// what was NOT written as easily as on what was.
type recordingFacts struct {
	nopFacts
	puts []string
	when []time.Time
	err  error
}

func (r *recordingFacts) PutFacts(_ context.Context, _, tag string, facts []byte, now time.Time) error {
	if r.err != nil {
		return r.err
	}
	r.puts = append(r.puts, tag+":"+string(facts))
	r.when = append(r.when, now)
	return nil
}

// RecordFacts is the enrolment path: the imaging station captures a
// nixos-facter document before the device has ever run an agent, so the
// console can show its hardware from the first minute.
//
// It is a write of operator-supplied bytes into the observed plane, and it
// had no test. Its four refusals are the whole of its validation, and each
// one fails in a way the caller would not notice: a nameless document lands
// under an empty tag, a malformed one is stored and breaks whatever reads it
// later, and an unbounded one is a memory cost per device.
func TestRecordFactsRefusesWhatItCannotStoreSafely(t *testing.T) {
	ctx := context.Background()
	good := []byte(`{"hardware":{"cpu":"i7"}}`)

	t.Run("a valid document is stored under its tag", func(t *testing.T) {
		f := &recordingFacts{}
		inv := NewInventoryService(newMemStatus(), f, clockAt{time.Unix(1700, 0)}, "default")
		if err := inv.RecordFacts(ctx, "lt-1", good); err != nil {
			t.Fatal(err)
		}
		if len(f.puts) != 1 || f.puts[0] != `lt-1:{"hardware":{"cpu":"i7"}}` {
			t.Fatalf("stored %v", f.puts)
		}
		// The clock is the service's, not the caller's: a station with a
		// wrong clock must not backdate a device's hardware.
		if got := f.when[0]; !got.Equal(time.Unix(1700, 0)) {
			t.Errorf("timestamp %v, want the service clock", got)
		}
	})

	t.Run("no tag is refused rather than stored under an empty one", func(t *testing.T) {
		f := &recordingFacts{}
		inv := NewInventoryService(newMemStatus(), f, clockAt{time.Unix(1, 0)}, "default")
		if err := inv.RecordFacts(ctx, "", good); err == nil {
			t.Error("accepted a facts document with no device tag")
		}
		if len(f.puts) != 0 {
			t.Errorf("wrote anyway: %v", f.puts)
		}
	})

	// Empty is not an error and not a write. A station that captured nothing
	// has nothing to say, and turning that into a failure would fail an
	// enrolment over a missing nicety.
	t.Run("an empty document is a no-op, not a failure", func(t *testing.T) {
		f := &recordingFacts{}
		inv := NewInventoryService(newMemStatus(), f, clockAt{time.Unix(1, 0)}, "default")
		if err := inv.RecordFacts(ctx, "lt-1", nil); err != nil {
			t.Errorf("empty facts reported an error: %v", err)
		}
		if len(f.puts) != 0 {
			t.Errorf("wrote an empty document: %v", f.puts)
		}
	})

	t.Run("malformed JSON is refused before it reaches the store", func(t *testing.T) {
		f := &recordingFacts{}
		inv := NewInventoryService(newMemStatus(), f, clockAt{time.Unix(1, 0)}, "default")
		for _, bad := range [][]byte{
			[]byte(`{"hardware":`),
			[]byte(`not json at all`),
			[]byte(`{"a":1}{"b":2}`), // two documents in one body
		} {
			if err := inv.RecordFacts(ctx, "lt-1", bad); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
		if len(f.puts) != 0 {
			t.Errorf("stored something unparseable: %v", f.puts)
		}
	})

	t.Run("an oversized document is refused at the boundary", func(t *testing.T) {
		f := &recordingFacts{}
		inv := NewInventoryService(newMemStatus(), f, clockAt{time.Unix(1, 0)}, "default")

		// Exactly at the limit and valid: accepted. Padding a JSON string to
		// the byte, so the test is about the size and not about the shape.
		const head, tail = `{"pad":"`, `"}`
		atLimit := []byte(head + strings.Repeat("x", maxFactsBytes-len(head)-len(tail)) + tail)
		if len(atLimit) != maxFactsBytes {
			t.Fatalf("fixture is %d bytes, want %d", len(atLimit), maxFactsBytes)
		}
		if err := inv.RecordFacts(ctx, "lt-1", atLimit); err != nil {
			t.Errorf("refused a document of exactly the limit: %v", err)
		}

		// One byte over, and still VALID JSON. The first version appended a
		// byte after the closing brace, which made the document malformed
		// too, so json.Valid rejected it and the size check was never the
		// thing under test. It survived its own mutation.
		over := []byte(head + strings.Repeat("x", maxFactsBytes+1-len(head)-len(tail)) + tail)
		if len(over) != maxFactsBytes+1 {
			t.Fatalf("fixture is %d bytes, want %d", len(over), maxFactsBytes+1)
		}
		if !json.Valid(over) {
			t.Fatal("the over-limit fixture is malformed, so this tests the wrong guard")
		}
		if err := inv.RecordFacts(ctx, "lt-1", over); err == nil {
			t.Error("accepted a document one byte over the limit")
		}
		if len(f.puts) != 1 {
			t.Errorf("%d writes, want only the one at the limit", len(f.puts))
		}
	})

	t.Run("a store failure reaches the caller", func(t *testing.T) {
		f := &recordingFacts{err: errors.New("disk is full")}
		inv := NewInventoryService(newMemStatus(), f, clockAt{time.Unix(1, 0)}, "default")
		if err := inv.RecordFacts(ctx, "lt-1", good); err == nil {
			t.Error("a failed write was reported as success")
		}
	})
}
