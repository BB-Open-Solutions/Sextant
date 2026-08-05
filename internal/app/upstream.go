package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

	// Delivery (phase two, optional): with these set, a staged CR also gets
	// its CONTENT - the runner computes the flake.lock pinning the input to
	// the new head, EditFile commits it on the branch and Submit proves the
	// branch with the ordinary build gate. Without them the CR stays an
	// empty draft for a human to fill.
	bump      func(ctx context.Context, input string) ([]byte, string, error)
	editFile  func(ctx context.Context, id, path string, content []byte, msg string, a ports.Author) error
	submit    func(ctx context.Context, id string) (change.CR, error)
	inputName string

	// checked observes a completed check (metrics); nil-safe optional.
	checked func(time.Time)

	// list and abandon retire core updates that a newer one supersedes.
	// Optional: without them the older CRs simply stay open, which is what
	// happened until 2026-08-05 - four core updates in the review queue, three
	// of them pointing at upstream revisions already overtaken, each still
	// offering a Submit button with nothing saying it was stale.
	list    func(ctx context.Context) ([]change.CR, error)
	abandon func(ctx context.Context, id string) (change.CR, error)
}

// WithSupersede lets the watcher retire core updates that a newer upstream
// head has overtaken. They are not alternatives to choose between: each one pins
// the core to the revision it was staged for, so merging an older one after a
// newer one walks the fleet's core backwards - or collides on the lock file.
// Neither outcome is something a review queue should offer silently.
func (s *UpstreamService) WithSupersede(
	list func(context.Context) ([]change.CR, error),
	abandon func(context.Context, string) (change.CR, error)) *UpstreamService {
	s.list, s.abandon = list, abandon
	return s
}

// WithCheckMetric records each completed check's time, so operators can
// alert on a watcher that silently stopped (leader lost, forge down).
func (s *UpstreamService) WithCheckMetric(f func(time.Time)) *UpstreamService {
	s.checked = f
	return s
}

// WithDelivery arms phase two: computing and staging the flake bump inside
// the CR, then submitting it through the build gate.
func (s *UpstreamService) WithDelivery(
	bump func(context.Context, string) ([]byte, string, error),
	editFile func(context.Context, string, string, []byte, string, ports.Author) error,
	submit func(context.Context, string) (change.CR, error),
	inputName string) *UpstreamService {
	s.bump, s.editFile, s.submit = bump, editFile, submit
	s.inputName = inputName
	return s
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
	if last == "" {
		// First run: adopt the current upstream head as the baseline instead
		// of staging a "core update" that is not one.
		s.log.Info("upstream baseline recorded", "rev", head)
		return s.seen.PutUpstream(ctx, head)
	}
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	id := "core-" + short
	title := "Core update " + short
	author := ports.Author{Name: "sextant-upstream", Email: "upstream@sextant"}
	// Retire the ones this head overtakes BEFORE staging the new one, so the
	// queue never shows two live core updates at once - not even briefly.
	s.supersede(ctx, id)
	if _, err := s.open(ctx, id, title, author); err != nil {
		// "already exists" means an operator (or an earlier run whose seen-
		// write was lost) staged it: record and move on rather than error.
		s.log.Info("core update CR not opened", "id", id, "reason", err)
	} else {
		s.log.Info("core update staged", "id", id, "rev", head)
		s.deliver(ctx, id, author)
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

// supersede abandons every still-open core update except keep. Best-effort:
// failing to tidy the queue must not stop the new core update from being
// staged, because the stale entry is the lesser problem of the two.
//
// Only core updates (the "core-" prefix the watcher itself mints) are touched.
// A human's change that happens to be open is none of this function's business.
func (s *UpstreamService) supersede(ctx context.Context, keep string) {
	if s.list == nil || s.abandon == nil {
		return
	}
	crs, err := s.list(ctx)
	if err != nil {
		s.log.Warn("supersede: cannot list changes", "err", err)
		return
	}
	for _, cr := range crs {
		if cr.ID == keep || !strings.HasPrefix(cr.ID, "core-") {
			continue
		}
		// Merged and abandoned are already settled; Building is mid-flight and
		// abandoning it would race the worker that is proving it.
		switch cr.Status {
		case change.Draft, change.Failed, change.Ready:
		default:
			continue
		}
		if _, err := s.abandon(ctx, cr.ID); err != nil {
			s.log.Warn("supersede: cannot abandon", "id", cr.ID, "err", err)
			continue
		}
		s.log.Info("core update superseded", "id", cr.ID, "by", keep)
	}
}

// deliver fills the staged CR (phase two): runner computes the lock, the
// change branch records it, Submit proves it. Best-effort - a failure leaves
// a draft CR plus a log line, never a broken watcher.
func (s *UpstreamService) deliver(ctx context.Context, id string, author ports.Author) {
	if s.bump == nil || s.editFile == nil || s.submit == nil {
		return
	}
	lock, rev, err := s.bump(ctx, s.inputName)
	if err != nil {
		s.log.Warn("core bump failed; CR left as draft", "id", id, "err", err)
		return
	}
	msg := "core: pin " + s.inputName + " to " + shortRev(rev)
	if err := s.editFile(ctx, id, "flake.lock", lock, msg, author); err != nil {
		s.log.Warn("staging flake.lock failed; CR left as draft", "id", id, "err", err)
		return
	}
	if _, err := s.submit(ctx, id); err != nil {
		s.log.Warn("core CR submit failed; review it by hand", "id", id, "err", err)
		return
	}
	s.log.Info("core update ready for review", "id", id, "rev", rev)
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
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
			} else if s.checked != nil {
				s.checked(time.Now())
			}
		}
	}
}
