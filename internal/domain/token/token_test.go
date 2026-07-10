package token

import (
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

var t0 = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func TestMintAndVerify(t *testing.T) {
	tok, secret, err := Mint("id1", "ci token", Personal, "sub-1",
		[]string{"editors"}, "", t0, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeToken(secret) {
		t.Errorf("secret has no prefix: %q", secret)
	}
	if tok.Hash == secret || tok.Hash == "" {
		t.Fatal("secret stored in the clear or hash empty")
	}
	if !tok.Verify(secret) {
		t.Error("correct secret rejected")
	}
	if tok.Verify(secret + "x") {
		t.Error("wrong secret accepted")
	}
	if tok.Verify("") {
		t.Error("empty secret accepted")
	}
	if !tok.Expires.Equal(t0.Add(24 * time.Hour)) {
		t.Errorf("expiry = %v", tok.Expires)
	}
}

func TestMintValidation(t *testing.T) {
	bad := []struct {
		name                  string
		id, tname, subj, ceil string
		kind                  Kind
		ttl                   time.Duration
	}{
		{"no id", "", "n", "s", "", Personal, time.Hour},
		{"no name", "i", "", "s", "", Personal, time.Hour},
		{"no subject", "i", "n", "", "", Personal, time.Hour},
		{"bad kind", "i", "n", "s", "", "robot", time.Hour},
		{"bad ceiling", "i", "n", "s", "wizard", Personal, time.Hour},
		{"no ttl", "i", "n", "s", "", Personal, 0},
		{"negative ttl", "i", "n", "s", "", Personal, -time.Hour},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Mint(tc.id, tc.tname, tc.kind, tc.subj, nil, tc.ceil, t0, tc.ttl); err == nil {
				t.Error("accepted invalid token")
			}
		})
	}
}

func TestExpired(t *testing.T) {
	tok, _, _ := Mint("i", "n", Personal, "s", nil, "", t0, time.Hour)
	if tok.Expired(t0.Add(30 * time.Minute)) {
		t.Error("not yet expired")
	}
	if !tok.Expired(t0.Add(time.Hour)) {
		t.Error("should be expired at expiry")
	}
	if !tok.Expired(t0.Add(2 * time.Hour)) {
		t.Error("should be expired past expiry")
	}
}

func TestUserProjection(t *testing.T) {
	p, _, _ := Mint("i", "ada", Personal, "sub-ada", []string{"fo-editors"}, "", t0, time.Hour)
	u := p.User()
	if u.Subject != "sub-ada" || len(u.Groups) != 1 || u.Groups[0] != "fo-editors" {
		t.Fatalf("personal user = %+v", u)
	}
	if u.Service {
		t.Error("personal token must not be a service principal")
	}

	s, _, _ := Mint("i2", "ci", Service, "svc:ci", []string{"ci-bots"}, "", t0, time.Hour)
	su := s.User()
	if su.Subject != "svc:ci" || len(su.Groups) != 1 {
		t.Fatalf("service user = %+v", su)
	}
}

func TestCeiling(t *testing.T) {
	tok, _, _ := Mint("i", "dash", Personal, "s", nil, "viewer", t0, time.Hour)
	r, ok := tok.CeilingRole()
	if !ok || r != identity.Viewer {
		t.Fatalf("ceiling = %v %v", r, ok)
	}
	none, _, _ := Mint("i2", "full", Personal, "s", nil, "", t0, time.Hour)
	if _, ok := none.CeilingRole(); ok {
		t.Error("no ceiling should report false")
	}
}

func TestHashUniquePerMint(t *testing.T) {
	// Same inputs must still yield different hashes (random salt) and both
	// verify - no hash reuse leaking equality.
	a, sa, _ := Mint("i", "n", Personal, "s", nil, "", t0, time.Hour)
	b, sb, _ := Mint("i", "n", Personal, "s", nil, "", t0, time.Hour)
	if a.Hash == b.Hash {
		t.Error("identical hashes across mints (salt not random?)")
	}
	if !a.Verify(sa) || !b.Verify(sb) {
		t.Error("verify failed")
	}
	if a.Verify(sb) {
		t.Error("token verified another token's secret")
	}
}

func TestDummyVerifyAlwaysFalse(t *testing.T) {
	// It must burn work but never authenticate.
	if DummyVerify("sxt_x_whatever") {
		t.Error("dummy verify returned true")
	}
	if DummyVerify("") {
		t.Error("dummy verify true for empty")
	}
}
