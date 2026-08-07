package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

// erasure_test.go runs against real Postgres because the risk here is not
// that the delete fails - it is that a predicate is one clause too wide and
// takes somebody else's data with it. Only the statement can prove that.

func seedPerson(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, n := range []notify.Notification{
		{ID: "n1", Tenant: "t1", Recipient: "sub-ada", Kind: notify.ApprovalNeeded, Title: "hers", CreatedAt: now},
		{ID: "n2", Tenant: "t1", Recipient: "sub-bob", Kind: notify.ApprovalNeeded, Title: "his", CreatedAt: now},
		// Addressed to a GROUP: not personal data about anybody, and the row
		// an over-wide predicate would take.
		{ID: "n3", Tenant: "t1", Audience: "dawo-beheer", Kind: notify.ApprovalNeeded, Title: "everyone's", CreatedAt: now},
	} {
		if err := s.Add(ctx, n); err != nil {
			t.Fatalf("seed notification %s: %v", n.ID, err)
		}
	}
	for _, r := range []elevation.Request{
		{ID: "e1", Tag: "lt-1", User: "ada", Action: "a", Reason: "hers", State: elevation.Pending, Created: now},
		{ID: "e2", Tag: "lt-2", User: "bob", Action: "a", Reason: "his", State: elevation.Pending, Created: now},
	} {
		if err := s.Elevation().Put(ctx, "t1", r); err != nil {
			t.Fatalf("seed elevation %s: %v", r.ID, err)
		}
	}
}

func TestErasureTakesOnePersonAndNobodyElse(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedPerson(t, s)

	// The preview must count exactly what the erase will take.
	pre, err := s.CountPersonalData(ctx, "t1", "sub-ada", "ada")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if pre.Notifications != 1 {
		t.Errorf("counted %d notifications, want 1 (hers only, not the group one)", pre.Notifications)
	}
	if pre.Elevation != 1 {
		t.Errorf("counted %d elevation requests, want 1", pre.Elevation)
	}

	got, err := s.ErasePersonalData(ctx, "t1", "sub-ada", "ada")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if got.Notifications != pre.Notifications || got.Elevation != pre.Elevation {
		t.Errorf("erase removed %+v but the preview promised %+v - an operator confirmed the wrong thing", got, pre)
	}

	// Bob is untouched.
	bob, err := s.CountPersonalData(ctx, "t1", "sub-bob", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if bob.Notifications != 1 || bob.Elevation != 1 {
		t.Errorf("erasing Ada took Bob's data too: %+v", bob)
	}
	// And so is the group notification, which belongs to nobody.
	all, err := s.ListFor(ctx, "t1", "sub-bob", []string{"dawo-beheer"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var group bool
	for _, n := range all {
		if n.ID == "n3" {
			group = true
		}
	}
	if !group {
		t.Error("the group notification was erased with a person; it is nobody's personal data")
	}
}

// TestErasureWithABlankIdentifierTakesNothing is the dangerous case. An
// empty subject matches every row whose subject column is empty - and a
// group notification has exactly that shape, so a blank run would delete
// the organisation's notifications and report it as erasing one person.
func TestErasureWithABlankIdentifierTakesNothing(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedPerson(t, s)

	got, err := s.ErasePersonalData(ctx, "t1", "", "")
	if err != nil {
		t.Fatalf("blank erase: %v", err)
	}
	if got.Total() != 0 {
		t.Errorf("a blank erasure removed %d rows", got.Total())
	}
	// Everything still there.
	for subject, user := range map[string]string{"sub-ada": "ada", "sub-bob": "bob"} {
		c, err := s.CountPersonalData(ctx, "t1", subject, user)
		if err != nil {
			t.Fatal(err)
		}
		if c.Notifications != 1 || c.Elevation != 1 {
			t.Errorf("a blank erasure removed %s's data: %+v", subject, c)
		}
	}
}

// TestErasureCountsDecisionsSeparately: a request this person decided for
// somebody else is the other person's evidence, so it is counted and left.
func TestErasureCountsDecisionsSeparately(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Bob raised it; Ada decided it.
	r := elevation.Request{ID: "e9", Tag: "lt-2", User: "bob", Action: "a",
		Reason: "his", State: elevation.Pending, Created: now}
	if err := s.Elevation().Put(ctx, "t1", r); err != nil {
		t.Fatal(err)
	}
	decided, err := r.Decide(true, "ada", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Elevation().Put(ctx, "t1", decided); err != nil {
		t.Fatal(err)
	}

	c, err := s.CountPersonalData(ctx, "t1", "sub-ada", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if c.Elevation != 0 {
		t.Errorf("Ada raised %d requests, want 0 - she only decided one", c.Elevation)
	}
	if c.ElevationDecided != 1 {
		t.Errorf("counted %d decisions by Ada, want 1", c.ElevationDecided)
	}

	if _, err := s.ErasePersonalData(ctx, "t1", "sub-ada", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Elevation().Get(ctx, "t1", "e9"); !ok {
		t.Error("erasing Ada destroyed Bob's record of who approved his access")
	}
}

// TestABlankSubjectCannotMatchAnEmptyRow pins why the non-empty check in
// ErasePersonalData is not redundant.
//
// The notification predicate is self-guarding: `recipient = $1 AND recipient
// <> ”` is contradictory when $1 is empty, so it can never match. The other
// three are NOT. `subject = ”` matches any row whose subject column is
// empty, and rows like that exist - a seen_users entry written before an IdP
// supplied a subject, a preference row from a migration. Without the guard a
// blank erasure would take them and report it as erasing one person.
func TestABlankSubjectCannotMatchAnEmptyRow(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// A row with an empty subject, the shape the guard exists for.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO seen_users (tenant, subject, email, name, groups, seen)
		 VALUES ('t1', '', 'nobody@example.org', 'nobody', '{}', now())`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := s.ErasePersonalData(ctx, "t1", "", "")
	if err != nil {
		t.Fatalf("blank erase: %v", err)
	}
	if got.SeenUser != 0 {
		t.Errorf("a blank erasure removed %d seen_users rows", got.SeenUser)
	}

	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM seen_users WHERE tenant = 't1' AND subject = ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the empty-subject row was erased by an erasure that named nobody")
	}
}
