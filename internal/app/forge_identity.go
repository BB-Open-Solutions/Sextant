package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/forge"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// forge_identity.go: the credential the console pushes to the forge with, and
// the one thing that made it worth building - an admin can rotate it from the
// console (ADR 0022).
//
// Before this, the credential arrived as a netrc in a mounted secret. Rotating
// it meant editing a Kubernetes secret, so whoever could rotate it needed
// cluster access, and in practice nobody did: measured at bb-open on
// 2026-08-06, the console had been pushing as a named person's account for
// months (audit finding H2). A credential that is awkward to rotate is a
// credential nobody rotates.
//
// The console writes the netrc onto its OWN volume - the same one holding the
// overlay clone - so rotation needs no Kubernetes API access, no extra RBAC
// and no restart: git reads the file per invocation, so the next push uses the
// new credential.

// ForgeIdentityService stores and applies the console's forge credential.
type ForgeIdentityService struct {
	store  ports.ForgeIdentityStore
	sealer secretbox.Sealer
	tenant string
	// netrcPath is the file git reads. Empty disables writing, which is what
	// a deployment without a home directory (tests, --repo-less probes) gets.
	netrcPath string
	log       *slog.Logger
	now       func() time.Time
}

// NewForgeIdentityService wires the service. netrcPath is usually
// $HOME/.netrc; an empty path leaves the stored identity inert rather than
// guessing a location, because guessing wrong writes a credential somewhere
// nothing reads and reports success.
func NewForgeIdentityService(store ports.ForgeIdentityStore, sealer secretbox.Sealer,
	tenant, netrcPath string, log *slog.Logger) *ForgeIdentityService {
	return &ForgeIdentityService{
		store: store, sealer: sealer, tenant: tenant,
		netrcPath: netrcPath, log: log, now: time.Now,
	}
}

// Enabled reports whether an identity can be stored at all. Without a sealing
// key there is nowhere safe to put the token, and without a netrc path there
// is nothing that would read it.
func (s *ForgeIdentityService) Enabled() bool {
	return s != nil && s.sealer.Enabled() && s.netrcPath != ""
}

// Current returns the stored identity WITHOUT its token. There is no method
// that returns the token to a caller outside this file: the console can
// replace the credential and say who did, and that is all a rotation UI needs.
func (s *ForgeIdentityService) Current(ctx context.Context) (forge.Identity, bool, error) {
	if s == nil || s.store == nil {
		return forge.Identity{}, false, nil
	}
	id, ok, err := s.store.GetForgeIdentity(ctx, s.tenant)
	if err != nil || !ok {
		return forge.Identity{}, false, err
	}
	id.TokenEnc = nil
	return id, true, nil
}

// Set validates, seals and stores a new credential, then applies it. The
// order matters: applying last means a failure to write the file leaves the
// store and the file disagreeing, which Apply on the next start repairs -
// whereas writing first and failing to store would leave a live credential
// nothing knows about.
func (s *ForgeIdentityService) Set(ctx context.Context, host, username, token, by string) error {
	if !s.Enabled() {
		return fmt.Errorf("forge credential cannot be stored: this deployment has no sealing key or no writable home")
	}
	if err := forge.Validate(host, username, token); err != nil {
		return err
	}
	sealed, err := s.sealer.Seal([]byte(token))
	if err != nil {
		return fmt.Errorf("seal forge token: %w", err)
	}
	id := forge.Identity{Host: host, Username: username, TokenEnc: sealed, UpdatedBy: by}
	if err := s.store.PutForgeIdentity(ctx, s.tenant, id); err != nil {
		return fmt.Errorf("store forge identity: %w", err)
	}
	if err := s.writeNetrc(host, username, token); err != nil {
		return err
	}
	// Deliberately logs the host and the account but never the token, and
	// says who did it: this is the audit trail H2 was missing.
	s.logger().Info("forge credential rotated", "host", host, "username", username, "by", by)
	return nil
}

// Clear removes the stored identity and the netrc line it wrote, returning
// the deployment to its mounted credential. The file is removed rather than
// emptied: an empty netrc and a missing one behave the same to git, and a
// leftover empty file invites somebody to conclude the credential is gone
// when a mount may still be supplying one.
func (s *ForgeIdentityService) Clear(ctx context.Context, by string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if err := s.store.DeleteForgeIdentity(ctx, s.tenant); err != nil {
		return err
	}
	if s.netrcPath != "" {
		if err := os.Remove(s.netrcPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", s.netrcPath, err)
		}
	}
	s.logger().Info("forge credential cleared; falling back to the mounted one", "by", by)
	return nil
}

// Apply writes the stored credential to the netrc at startup. No stored
// identity is not an error: it means the deployment uses its mounted
// credential, which is the pre-2026-08 behaviour and stays supported.
//
// It reports whether it wrote anything, so the caller can say which
// credential the console is actually pushing with instead of leaving that to
// be inferred.
func (s *ForgeIdentityService) Apply(ctx context.Context) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	id, ok, err := s.store.GetForgeIdentity(ctx, s.tenant)
	if err != nil || !ok {
		return false, err
	}
	token, err := s.sealer.Open(id.TokenEnc)
	if err != nil {
		// A key rotation that orphaned this blob must not take the console
		// down: the mounted credential (if any) still works, and an operator
		// can re-enter the token. Loud, not fatal.
		return false, fmt.Errorf("stored forge token cannot be decrypted (was the sealing key rotated?): %w", err)
	}
	if err := s.writeNetrc(id.Host, id.Username, string(token)); err != nil {
		return false, err
	}
	return true, nil
}

// writeNetrc replaces the netrc atomically. A push may be running: git opens
// the file per invocation, so a truncate-then-write would give a concurrent
// git a half-written line and an authentication failure that looks like a
// wrong password. Write a sibling temp file and rename over the target, which
// is atomic within a directory.
func (s *ForgeIdentityService) writeNetrc(host, username, token string) error {
	dir := filepath.Dir(s.netrcPath)
	f, err := os.CreateTemp(dir, ".netrc-*")
	if err != nil {
		return fmt.Errorf("write netrc: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeded
	// Mode before content: git refuses a group/world-readable netrc on some
	// platforms, and more to the point the token must never exist on disk
	// readable by anything else, not even briefly.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("write netrc: %w", err)
	}
	if _, err := f.WriteString(forge.Netrc(host, username, token)); err != nil {
		_ = f.Close()
		return fmt.Errorf("write netrc: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write netrc: %w", err)
	}
	if err := os.Rename(tmp, s.netrcPath); err != nil {
		return fmt.Errorf("write netrc: %w", err)
	}
	return nil
}

func (s *ForgeIdentityService) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
