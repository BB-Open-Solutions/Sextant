package app

import (
	"context"
	"log/slog"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// WireAutoRollout makes saving a change roll it out (the Intune model, with
// our waves underneath): after a change merges, a delivery run starts for
// exactly its blast radius - the test wave first, then only the groups the
// change touches; an org-wide change walks the full ladder. The org can opt
// back into hand-started runs with assurance.manualRolloutOnly.
func WireAutoRollout(changes *ChangeService, rollouts *RolloutService,
	cfg *ConfigService, n Notifier, audience []string, log *slog.Logger) {
	changes.WithAfterMerge(func(ctx context.Context, cr change.CR) {
		f := cfg.Fleet()
		if asr := f.Assurance; asr != nil && asr.ManualRolloutOnly {
			return // the org chose hand-started runs
		}
		opts := StartOpts{}
		if !cr.WholeFleet {
			groups := deliveryGroups(f, cr.Hosts)
			if len(groups) == 0 {
				return // nothing deployable was touched
			}
			opts.Groups = groups
		}
		target := cfg.Head(ctx)
		if target == "" {
			log.Warn("auto-rollout skipped: unknown HEAD", "change", cr.ID)
			return
		}
		author := ports.Author{Name: "sextant-delivery", Email: "delivery@sextant"}
		if _, err := rollouts.StartWith(ctx, target, opts, author); err != nil {
			// Usually "a rollout is already active": the merge is safe on main
			// and delivers with that run's successor; tell the owners instead
			// of failing the merge path.
			log.Info("auto-rollout not started", "change", cr.ID, "reason", err)
			emitAll(ctx, n, audience, notify.Notification{
				Kind:  notify.ApprovalNeeded,
				Title: "Merged change awaits delivery",
				Body:  "Change " + cr.ID + " merged but a rollout is already running. Start its delivery from Updates when the current run ends.",
				Link:  "/updates",
			})
			return
		}
		log.Info("auto-rollout started", "change", cr.ID, "groups", opts.Groups, "target", target)
		emitAll(ctx, n, audience, notify.Notification{
			Kind:  notify.RolloutDone, // informational; the monitor shows live state
			Title: "Delivery started: " + cr.Title,
			Body:  "The change rolls out via the test wave first. Follow it on the rollout monitor.",
			Link:  "/updates/rollout",
		})
	})
}

// deliveryGroups is the blast radius as groups: each touched host's primary
// group, deduplicated and sorted (deterministic wave content).
func deliveryGroups(f *fleet.Fleet, hosts []string) []string {
	seen := map[string]bool{}
	for _, h := range hosts {
		if d, ok := f.Devices[h]; ok && len(d.Groups) > 0 {
			seen[d.Groups[0]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func emitAll(ctx context.Context, n Notifier, audience []string, base notify.Notification) {
	if n == nil {
		return
	}
	for _, g := range audience {
		msg := base
		msg.Audience = g
		_ = n.Emit(ctx, msg)
	}
}
