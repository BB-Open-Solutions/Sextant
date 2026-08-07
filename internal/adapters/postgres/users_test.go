package postgres

import (
	"context"
	"testing"
	"time"
)

// users_test.go covers the seen-users address book: the cache of operator
// identities that lets the notifier deliver by e-mail.
//
// Two of the properties here are promises made outside the code. The
// processing agreement puts a 365-day term on operator identities, and the
// register calls this cache a convenience rather than a source of truth.
// Both are statements about what these DELETE and UPSERT statements do, so
// they are tested against real SQL rather than a fake.

// backdate moves a row's last-seen timestamp. RecordUser always stamps now,
// which is correct for the product and useless for testing a cutoff.
func backdate(t *testing.T, s *Store, tenant, subject string, when time.Time) {
	t.Helper()
	tag, err := s.pool.Exec(context.Background(),
		`UPDATE seen_users SET seen = $1 WHERE tenant = $2 AND subject = $3`,
		when, tenant, subject)
	if err != nil {
		t.Fatalf("backdate %s: %v", subject, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("backdate %s touched %d rows, want 1 - the seed did not land",
			subject, tag.RowsAffected())
	}
}

func TestSeenUserRetentionKeepsTheRecentAndSparesOtherTenants(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for _, u := range []struct{ tenant, subject, email string }{
		{"t1", "stale", "stale@example.org"},
		{"t1", "active", "active@example.org"},
		{"t2", "stale", "neighbour@example.org"},
	} {
		if err := s.RecordUser(ctx, u.tenant, u.subject, u.email, u.subject, []string{"ops"}); err != nil {
			t.Fatalf("seed %s/%s: %v", u.tenant, u.subject, err)
		}
	}
	// Both stale rows sit well past the term; only t1 is swept.
	backdate(t, s, "t1", "stale", now.Add(-400*24*time.Hour))
	backdate(t, s, "t2", "stale", now.Add(-400*24*time.Hour))

	got, err := s.DeleteSeenUsersBefore(ctx, "t1", now.Add(-365*24*time.Hour))
	if err != nil {
		t.Fatalf("delete seen users: %v", err)
	}
	if got != 1 {
		t.Fatalf("removed %d identities, want 1", got)
	}

	if _, ok, err := s.EmailForSubject(ctx, "t1", "stale"); err != nil || ok {
		t.Errorf("the stale identity survived the sweep (ok=%v, err=%v)", ok, err)
	}
	if _, ok, err := s.EmailForSubject(ctx, "t1", "active"); err != nil || !ok {
		t.Errorf("the sweep took an identity seen this second (ok=%v, err=%v)", ok, err)
	}
	// The neighbour is the point of the tenant column. A sweep that reaches
	// across tenants deletes data belonging to somebody who never asked.
	if _, ok, err := s.EmailForSubject(ctx, "t2", "stale"); err != nil || !ok {
		t.Errorf("the sweep crossed into another tenant (ok=%v, err=%v)", ok, err)
	}
}

func TestRecordUserReplacesGroupsSoALostMembershipStopsTheMail(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.RecordUser(ctx, "t1", "ada", "ada@example.org", "Ada", []string{"ops", "approvers"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EmailsForAudience(ctx, "t1", "approvers"); err != nil || len(got) != 1 {
		t.Fatalf("approvers = %v (err=%v), want the one address", got, err)
	}

	// The next login reveals the membership is gone. Merging the two sets
	// would keep mailing approval requests to somebody the directory no
	// longer trusts to approve them, and nothing else in the system would
	// notice: the address book is what the notifier reads.
	if err := s.RecordUser(ctx, "t1", "ada", "ada@example.org", "Ada", []string{"ops"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.EmailsForAudience(ctx, "t1", "approvers")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("approvers = %v, want none - the revoked membership still reaches her", got)
	}
	// The membership she kept must still work, or the test above would pass
	// on a store that simply lost the row.
	if ops, err := s.EmailsForAudience(ctx, "t1", "ops"); err != nil || len(ops) != 1 {
		t.Errorf("ops = %v (err=%v), want the address she still qualifies for", ops, err)
	}
}

func TestAddressBookAnswersPerTenant(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.RecordUser(ctx, "t1", "ada", "ada@example.org", "Ada", []string{"ops"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.EmailForSubject(ctx, "t2", "ada"); err != nil || ok {
		t.Errorf("subject %q resolved for a tenant that never saw her (ok=%v, err=%v)", "ada", ok, err)
	}
	if got, err := s.EmailsForAudience(ctx, "t2", "ops"); err != nil || len(got) != 0 {
		t.Errorf("audience for another tenant = %v (err=%v), want none", got, err)
	}
}

func TestBlankAddressCountsAsNoAddress(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// A directory entry without mail is normal, and the caller must not try
	// to send to "". Both routes have to agree on that.
	if err := s.RecordUser(ctx, "t1", "grace", "", "Grace", []string{"ops"}); err != nil {
		t.Fatal(err)
	}
	if addr, ok, err := s.EmailForSubject(ctx, "t1", "grace"); err != nil || ok {
		t.Errorf("EmailForSubject = %q, ok=%v (err=%v), want no address", addr, ok, err)
	}
	if got, err := s.EmailsForAudience(ctx, "t1", "ops"); err != nil || len(got) != 0 {
		t.Errorf("EmailsForAudience = %v (err=%v), want none", got, err)
	}
}

func TestUnknownSubjectIsNotAnError(t *testing.T) {
	s := openStore(t)
	addr, ok, err := s.EmailForSubject(context.Background(), "t1", "nobody")
	if err != nil {
		t.Fatalf("a missing row must read as absent, not as a failure: %v", err)
	}
	if ok || addr != "" {
		t.Errorf("got %q, ok=%v, want empty and absent", addr, ok)
	}
}
