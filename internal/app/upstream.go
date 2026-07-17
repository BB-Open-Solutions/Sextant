package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// UpstreamService watches the core image repository (DAWO-NixOS) and, when a
// new release lands, stages a change request and tells the owners - the
// "automation prepares, a human approves" half of delivery-process q5. Phase
// one is detect + stage + notify; realising the flake bump inside the CR
// worktree is a gate-runner job (it needs nix), designed in
// docs/design/delivery-process.md §9 and wired in a later slice.
type UpstreamService struct {
	repoURL string
	// head resolves the upstream repo's current HEAD (adapters/git.RemoteHead
	// in production; a stub in tests).
	head func(ctx context.Context, url string) (string, error)
	// open stages the CR (ChangeService.Open in production).
	open func(ctx context.Context, id, title string, a ports.Author) (change.CR, error)
	// seen persists the last upstream revision already staged, so a restart
	// does not re-open the same CR.
	seen ports.UpstreamStore

	notifier Notifier
	audience []string
	log      *slog.Logger
}

// NewUpstreamService wires the upstream watcher.
func NewUpstreamService(repoURL string,
	head func(context.Context, string) (string, error),
	open func(context.Context, string, string, ports.Author) (change.CR, error),
	seen ports.UpstreamStore, log *slog.Logger) *UpstreamService {
	return &UpstreamService{repoURL: repoURL, head: head, open: open, seen: seen, log: log}
}

// WithNotifier attaches owner notifications ("a core update is staged").
func (s *UpstreamService) WithNotifier(n Notifier, audience []string) *UpstreamService {
	s.notifier = n
	s.audience = audience
	return s
}

// CheckOnce polls upstream once; a new revision stages exactly one CR.
func (s *UpstreamService) CheckOnce(ctx context.Context) error {
	head, err := s.head(ctx, s.repoURL)
	if err != nil {
		return fmt.Errorf("upstream %s: %w", s.repoURL, err)
	}
	if head == "" {
		return nil
	}
	last, err := s.seen.LastUpstream(ctx)
	if err != nil {
		return err
	}
	if head == last {
		return nil
	}
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	id := "core-" + short
	title := "Core update " + short
	author := ports.Author{Name: "sextant-upstream", Email: "upstream@sextant"}
	if _, err := s.open(ctx, id, title, author); err != nil {
		// "already exists" means an operator (or an earlier run whose seen-
		// write was lost) staged it: record and move on rather than error.
		s.log.Info("core update CR not opened", "id", id, "reason", err)
	} else {
		s.log.Info("core update staged", "id", id, "rev", head)
		if s.notifier != nil {
			for _, g := range s.audience {
				_ = s.notifier.Emit(ctx, notify.Notification{
					Audience: g, Kind: notify.ApprovalNeeded,
					Title: "Core update staged",
					Body:  "New core release " + short + ": change " + id + " is staged for review.",
					Link:  "/updates",
				})
			}
		}
	}
	return s.seen.PutUpstream(ctx, head)
}

// Run polls on an interval until ctx ends. Errors are logged, not fatal: a
// down upstream forge must not take the watcher with it.
func (s *UpstreamService) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.CheckOnce(ctx); err != nil {
				s.log.Warn("upstream check failed", "err", err)
			}
		}
	}
}
