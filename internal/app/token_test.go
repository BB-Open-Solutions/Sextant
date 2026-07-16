package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// memTokenStore is an in-memory ports.TokenStore for service-level tests.
type memTokenStore struct {
	mu sync.Mutex
	m  map[string]token.Token
}

func newMemTokenStore() *memTokenStore { return &memTokenStore{m: map[string]token.Token{}} }

func (s *memTokenStore) Put(_ context.Context, t token.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[t.ID] = t
	return nil
}
func (s *memTokenStore) Get(_ context.Context, id string) (token.Token, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[id]
	return t, ok, nil
}
func (s *memTokenStore) ListBySubject(_ context.Context, subject string) ([]token.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []token.Token
	for _, t := range s.m {
		if t.Subject == subject {
			out = append(out, t)
		}
	}
	return out, nil
}
func (s *memTokenStore) ListByKind(_ context.Context, kind token.Kind) ([]token.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []token.Token
	for _, t := range s.m {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out, nil
}
func (s *memTokenStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}
func (s *memTokenStore) TouchLastUsed(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.m[id]; ok {
		t.LastUsed = &at
		s.m[id] = t
	}
	return nil
}

func TestTokenServiceMintAndAuthenticate(t *testing.T) {
	store := newMemTokenStore()
	svc := NewTokenService(store, newFakeClock(testT0), time.Hour)
	ctx := context.Background()

	tok, secret, err := svc.Mint(ctx, MintRequest{
		ID: "ada-ci", Name: "Ada CI", Kind: token.Personal,
		Subject: "sub-ada", Groups: []string{"fo-editors"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.Expires.IsZero() {
		t.Fatal("no expiry set")
	}

	// The personal token authenticates AS its owner (same groups).
	u, ceiling, ok := svc.Authenticate(ctx, secret)
	if !ok || u.Subject != "sub-ada" || len(u.Groups) != 1 {
		t.Fatalf("auth = %+v %v %v", u, ceiling, ok)
	}
	if ceiling != identity.None {
		t.Errorf("no ceiling requested, got %v", ceiling)
	}

	// Wrong / unknown / malformed secrets fail.
	if _, _, ok := svc.Authenticate(ctx, secret+"x"); ok {
		t.Error("tampered secret accepted")
	}
	if _, _, ok := svc.Authenticate(ctx, "not-a-token"); ok {
		t.Error("non-token accepted")
	}
	if _, _, ok := svc.Authenticate(ctx, "sxt_ghost_abc"); ok {
		t.Error("unknown id accepted")
	}

	// Duplicate id refused.
	if _, _, err := svc.Mint(ctx, MintRequest{ID: "ada-ci", Name: "dup",
		Kind: token.Personal, Subject: "sub-ada"}); err == nil {
		t.Error("duplicate id accepted")
	}

	// Revoke kills it.
	if err := svc.Revoke(ctx, "ada-ci"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := svc.Authenticate(ctx, secret); ok {
		t.Error("revoked token still authenticates")
	}
}

// dirStub scripts a directory's group listing (or failure).
type dirStub struct {
	groups []ports.DirectoryGroup
	err    error
}

func (d dirStub) ListGroups(context.Context, string) ([]ports.DirectoryGroup, error) {
	return d.groups, d.err
}

// A personal token's group snapshot is pruned against the directory at
// authentication: rights tied to a DELETED group die immediately instead of
// living out the token's TTL. An unreachable directory keeps the snapshot
// (availability over best-effort hardening), and membership pruning never
// invents groups.
func TestTokenAuthenticatePrunesDeletedGroups(t *testing.T) {
	store := newMemTokenStore()
	svc := NewTokenService(store, newFakeClock(testT0), time.Hour)
	ctx := context.Background()
	_, secret, err := svc.Mint(ctx, MintRequest{
		ID: "ada-ci", Name: "Ada CI", Kind: token.Personal,
		Subject: "sub-ada", Groups: []string{"fo-editors", "fo-owners"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// fo-owners was deleted from the directory since mint.
	svc.WithDirectory(dirStub{groups: []ports.DirectoryGroup{{Name: "fo-editors"}}})
	u, _, ok := svc.Authenticate(ctx, secret)
	if !ok || len(u.Groups) != 1 || u.Groups[0] != "fo-editors" {
		t.Fatalf("deleted group survived the prune: %+v %v", u.Groups, ok)
	}

	// Directory down: the snapshot stands rather than locking clients out.
	svc.WithDirectory(dirStub{err: context.DeadlineExceeded})
	u, _, ok = svc.Authenticate(ctx, secret)
	if !ok || len(u.Groups) != 2 {
		t.Fatalf("unreachable directory changed the snapshot: %+v %v", u.Groups, ok)
	}
}

// The default TTL ceiling is 30 days: the snapshot's staleness bound.
func TestTokenDefaultTTLThirtyDays(t *testing.T) {
	svc := NewTokenService(newMemTokenStore(), newFakeClock(testT0), 0)
	if svc.DefaultTTL != 30*24*time.Hour {
		t.Fatalf("default TTL = %v, want 30 days", svc.DefaultTTL)
	}
}

func TestTokenServiceCeilingReturned(t *testing.T) {
	store := newMemTokenStore()
	svc := NewTokenService(store, newFakeClock(testT0), time.Hour)
	ctx := context.Background()
	_, secret, err := svc.Mint(ctx, MintRequest{
		ID: "dash", Name: "dashboard", Kind: token.Personal,
		Subject: "sub-ada", Groups: []string{"owners"}, Ceiling: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, ceiling, ok := svc.Authenticate(ctx, secret)
	if !ok || ceiling != identity.Viewer {
		t.Fatalf("ceiling = %v %v, want viewer", ceiling, ok)
	}
}

func TestTokenServiceExpiry(t *testing.T) {
	store := newMemTokenStore()
	clk := newFakeClock(testT0)
	svc := NewTokenService(store, clk, time.Hour)
	ctx := context.Background()
	_, secret, _ := svc.Mint(ctx, MintRequest{ID: "short", Name: "n",
		Kind: token.Personal, Subject: "s", TTL: time.Minute})

	if _, _, ok := svc.Authenticate(ctx, secret); !ok {
		t.Fatal("fresh token rejected")
	}
	clk.Advance(2 * time.Minute)
	if _, _, ok := svc.Authenticate(ctx, secret); ok {
		t.Fatal("expired token accepted")
	}
}
