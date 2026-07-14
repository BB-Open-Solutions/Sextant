package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Notifier receives fleet events the change flow raises (a change is ready to
// review, a build failed, a change merged). Delivery is best-effort: an emit
// error never fails the change operation that triggered it.
type Notifier interface {
	Emit(ctx context.Context, n notify.Notification) error
}

// ChangeRepo is what the change flow needs from the git adapter: the plain
// config-repo operations plus branches, worktrees and merges.
type ChangeRepo interface {
	ports.ConfigRepo
	ports.BranchRepo
}

// OpenWorktree opens a ConfigRepo view on a linked worktree directory; the
// git adapter provides it. Injected so tests can substitute.
type OpenWorktree func(dir string) (ports.ConfigRepo, error)

// ChangeService runs the change-request flow: edits happen on a cr/<id>
// branch (in a linked worktree), the build gate proves the change, a merge
// lands it on main. State is durable in the ChangeStore; the git branch is
// the change itself.
type ChangeService struct {
	repo    ChangeRepo
	store   ports.ChangeStore
	gate    ports.Gate
	builder ports.Builder
	clock   ports.Clock
	open    OpenWorktree
	// cfg is refreshed after a merge so direct reads see the new config.
	cfg *ConfigService

	// notifier and approvers are optional: when set, the flow raises in-app
	// notifications on ready/failed/merged. approvers are the groups whose
	// members review changes (the ApprovalNeeded audience).
	notifier  Notifier
	approvers []string

	mu sync.Mutex // serializes the whole change flow (branch/worktree ops)
}

// NewChangeService wires the change flow.
func NewChangeService(repo ChangeRepo, store ports.ChangeStore, gate ports.Gate,
	builder ports.Builder, clock ports.Clock, open OpenWorktree, cfg *ConfigService) *ChangeService {
	return &ChangeService{repo: repo, store: store, gate: gate,
		builder: builder, clock: clock, open: open, cfg: cfg}
}

// WithNotifier attaches in-app notifications to the change flow. approvers are
// the groups whose members are asked to review a ready change. Returns the
// service for chaining at wiring time.
func (s *ChangeService) WithNotifier(n Notifier, approvers []string) *ChangeService {
	s.notifier = n
	s.approvers = approvers
	return s
}

// notify emits one best-effort notification; a nil notifier or an emit error
// is silently ignored - a message must never fail the change operation.
func (s *ChangeService) notify(ctx context.Context, n notify.Notification) {
	if s.notifier == nil {
		return
	}
	_ = s.notifier.Emit(ctx, n)
}

// worktreeDir is where a change's linked worktree lives: outside the main
// tree's tracked files, keyed by the validated id.
func (s *ChangeService) worktreeDir(id string) string {
	return filepath.Join(s.repo.Dir(), ".cr", id)
}

// Open starts a change request: a branch off the current HEAD plus a draft
// record. The author's subject is recorded for four-eyes enforcement.
func (s *ChangeService) Open(ctx context.Context, id, title string, a ports.Author) (change.CR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, err := change.New(id, title, a.Name, a.Subject, s.clock.Now())
	if err != nil {
		return change.CR{}, err
	}
	if _, exists, err := s.store.Get(ctx, id); err != nil {
		return change.CR{}, err
	} else if exists {
		return change.CR{}, fmt.Errorf("change %q already exists", id)
	}
	if err := s.repo.CreateBranch(ctx, cr.Branch); err != nil {
		return change.CR{}, err
	}
	if err := s.store.Put(ctx, cr); err != nil {
		// The branch now exists with no record behind it: retrying Open with
		// the same id would pass the existence check above then fail
		// CreateBranch (branch already exists), permanently wedging the id.
		// Roll the branch back so the id is clean to retry or forget.
		if derr := s.repo.DeleteBranch(ctx, cr.Branch); derr != nil {
			return change.CR{}, fmt.Errorf("%w (and rollback of branch %q failed: %w)", err, cr.Branch, derr)
		}
		return change.CR{}, err
	}
	return cr, nil
}

// Get returns one change request.
func (s *ChangeService) Get(ctx context.Context, id string) (change.CR, bool, error) {
	if err := change.ValidID(id); err != nil {
		return change.CR{}, false, err
	}
	return s.store.Get(ctx, id)
}

// List returns all change requests, newest first.
func (s *ChangeService) List(ctx context.Context) ([]change.CR, error) {
	return s.store.List(ctx)
}

// Edit applies a gated mutation on the change's branch (never on main).
// Only drafts and failed changes (being reworked) accept edits.
func (s *ChangeService) Edit(ctx context.Context, id string, mut fleet.Mutation, msg string, a ports.Author, hosts ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, err := s.mustGet(ctx, id)
	if err != nil {
		return err
	}
	if cr.Status != change.Draft && cr.Status != change.Failed {
		return fmt.Errorf("change %q is %s; only draft or failed changes accept edits", id, cr.Status)
	}
	wt, err := s.ensureWorktree(ctx, cr)
	if err != nil {
		return err
	}
	if _, err := applyTx(ctx, wt, s.gate, mut, msg, a, hosts); err != nil {
		return err
	}
	// A reworked failed change moves back to draft.
	if cr.Status == change.Failed {
		if err := cr.Transition(change.Draft, s.clock.Now()); err != nil {
			return err
		}
		cr.Error = ""
	}
	cr.Updated = s.clock.Now()
	return s.store.Put(ctx, cr)
}

// Submit runs the build gate on the change's branch. Green moves the change
// to ready; a rejection records the reason and moves it to failed.
// Synchronous by design at this tier; a job runner can wrap it later.
func (s *ChangeService) Submit(ctx context.Context, id string) (change.CR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, err := s.mustGet(ctx, id)
	if err != nil {
		return change.CR{}, err
	}
	// Prepare the worktree BEFORE committing the change to Building. A worktree
	// failure here leaves the change in its prior status (retryable), instead
	// of stranding it in Building - a state only Submit can leave, which would
	// then reject its own retry (Building -> Building is not a legal step).
	wt, err := s.ensureWorktree(ctx, cr)
	if err != nil {
		return change.CR{}, err
	}
	if err := cr.Transition(change.Building, s.clock.Now()); err != nil {
		return change.CR{}, err
	}
	if err := s.store.Put(ctx, cr); err != nil {
		return change.CR{}, err
	}

	if err := s.builder.Build(ctx, wt.Dir(), nil); err != nil {
		cr.Error = err.Error()
		if terr := cr.Transition(change.Failed, s.clock.Now()); terr != nil {
			return change.CR{}, terr
		}
		if cr.AuthorSubject != "" {
			s.notify(ctx, notify.Notification{
				Recipient: cr.AuthorSubject, Kind: notify.GateFailed,
				Title: fmt.Sprintf("Build failed: %s", cr.Title),
				Body:  "The nix gate refused this change. Open it to see why and rework it.",
				Link:  "/changes/" + cr.ID,
			})
		}
	} else {
		cr.Error = ""
		if terr := cr.Transition(change.Ready, s.clock.Now()); terr != nil {
			return change.CR{}, terr
		}
		for _, g := range s.approvers {
			s.notify(ctx, notify.Notification{
				Audience: g, Kind: notify.ApprovalNeeded,
				Title: fmt.Sprintf("Review needed: %s", cr.Title),
				Body:  fmt.Sprintf("%s submitted a change that passed the gate and awaits approval.", cr.Author),
				Link:  "/changes/" + cr.ID,
			})
		}
	}
	return cr, s.store.Put(ctx, cr)
}

// Merge lands a ready change on main with a merge commit (audit trail),
// removes its worktree and branch, and refreshes the config snapshot.
// Merged is reachable ONLY here: a status update can never fake a merge.
func (s *ChangeService) Merge(ctx context.Context, id string, a ports.Author) (change.CR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, err := s.mustGet(ctx, id)
	if err != nil {
		return change.CR{}, err
	}
	if cr.Status != change.Ready {
		return change.CR{}, fmt.Errorf("change %q is %s; only ready changes merge", id, cr.Status)
	}
	// Segregation of duties (ADR 0007): when the organisation requires
	// four-eyes, the author cannot approve their own change. Fail CLOSED - an
	// unidentifiable principal (empty subject on either side) can never satisfy
	// segregation of duties, so it must be rejected, not waved through.
	if asr := s.cfg.Fleet().Assurance; asr != nil && asr.RequireFourEyes {
		if cr.AuthorSubject == "" || a.Subject == "" {
			return change.CR{}, fmt.Errorf("four-eyes required: change %q needs an identified author and a distinct approver to merge", id)
		}
		if cr.AuthorSubject == a.Subject {
			return change.CR{}, fmt.Errorf("four-eyes required: change %q cannot be approved by its author", id)
		}
	}
	// The merge mutates the same main-branch working tree the config service
	// writes to, so it runs under that service's single-writer lock - a
	// concurrent Apply and Merge must not interleave on the shared index.
	// Once MergeNoFF lands, the merge is irreversible, so the Merged status is
	// persisted immediately (before the snapshot reload / remote push), keeping
	// the store in step with git even if a later step fails.
	if err := s.cfg.WithWriteLock(func() error {
		if err := s.repo.MergeNoFF(ctx, cr.Branch, fmt.Sprintf("merge change %s: %s", cr.ID, cr.Title), a); err != nil {
			return err
		}
		if err := cr.Transition(change.Merged, s.clock.Now()); err != nil {
			return err
		}
		if err := s.store.Put(ctx, cr); err != nil {
			return fmt.Errorf("merged, but recording the change status failed: %w", err)
		}
		s.cleanup(ctx, cr)
		if err := s.cfg.reload(); err != nil {
			return fmt.Errorf("merged, but snapshot reload failed: %w", err)
		}
		return nil
	}); err != nil {
		return change.CR{}, err
	}
	if s.repo.HasRemote() {
		if err := s.repo.Push(ctx); err != nil {
			return cr, fmt.Errorf("merged locally, push failed: %w", err)
		}
	}
	// Tell the author their change landed. The approver merged it, so the
	// author (who may differ under four-eyes) learns of it here.
	if cr.AuthorSubject != "" {
		s.notify(ctx, notify.Notification{
			Recipient: cr.AuthorSubject, Kind: notify.ChangeMerged,
			Title: fmt.Sprintf("Merged: %s", cr.Title),
			Body:  fmt.Sprintf("%s approved and merged your change. It will roll out with the next release.", a.Name),
			Link:  "/changes/" + cr.ID,
		})
	}
	return cr, nil
}

// Diff returns what merging the change would alter - the artifact an
// approver reviews (ADR 0007). Empty for changes without edits.
func (s *ChangeService) Diff(ctx context.Context, id string) (string, error) {
	cr, err := s.mustGet(ctx, id)
	if err != nil {
		return "", err
	}
	if !cr.Open() {
		return "", fmt.Errorf("change %q is %s; no pending diff", id, cr.Status)
	}
	return s.repo.Diff(ctx, cr.Branch)
}

// Abandon closes a change without merging and tears down its branch.
func (s *ChangeService) Abandon(ctx context.Context, id string) (change.CR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, err := s.mustGet(ctx, id)
	if err != nil {
		return change.CR{}, err
	}
	if err := cr.Transition(change.Abandoned, s.clock.Now()); err != nil {
		return change.CR{}, err
	}
	s.cleanup(ctx, cr)
	return cr, s.store.Put(ctx, cr)
}

func (s *ChangeService) mustGet(ctx context.Context, id string) (change.CR, error) {
	if err := change.ValidID(id); err != nil {
		return change.CR{}, err
	}
	cr, ok, err := s.store.Get(ctx, id)
	if err != nil {
		return change.CR{}, err
	}
	if !ok {
		return change.CR{}, fmt.Errorf("unknown change %q", id)
	}
	return cr, nil
}

// ensureWorktree returns a ConfigRepo view on the change's worktree,
// creating the worktree when absent.
func (s *ChangeService) ensureWorktree(ctx context.Context, cr change.CR) (ports.ConfigRepo, error) {
	dir := s.worktreeDir(cr.ID)
	if wt, err := s.open(dir); err == nil {
		return wt, nil
	}
	if err := s.repo.AddWorktree(ctx, dir, cr.Branch); err != nil {
		return nil, err
	}
	return s.open(dir)
}

// cleanup removes a finished change's worktree and branch (best effort:
// a leaked worktree is an inconvenience, not a correctness problem).
func (s *ChangeService) cleanup(ctx context.Context, cr change.CR) {
	_ = s.repo.RemoveWorktree(ctx, s.worktreeDir(cr.ID))
	_ = s.repo.DeleteBranch(ctx, cr.Branch)
}
