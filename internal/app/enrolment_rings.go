package app

// enrolment_rings.go: make a newly enrolled device exist at the revision it is
// imaged from.
//
// THE PROBLEM, found on hardware 2026-08-04. Enrolment writes the device to
// main. Imaging installs the revision the device's ring is PINNED to (#16, so
// a machine is not born ahead of its own ring). Those two are never the same
// commit, and not by accident: the rollout engine records each promotion as a
// commit on main, so main is permanently at least one commit past the pin it
// just wrote. A device enrolled after that pin therefore does not exist in the
// revision it is installed from, and nixos-anywhere fails with
//
//	error: flake '...?rev=<pin>' does not provide attribute
//	       'nixosConfigurations."<tag>".config.system.build.diskoScript'
//
// which names neither the cause nor the remedy.
//
// WHY THE OBVIOUS FIXES ARE WRONG. Installing from main is what #16 fixed: the
// device is then ahead of its ring, and comin refuses a head that is not a
// descendant of what it runs, so it stays frozen at its image-time generation.
// Cherry-picking the enrolment onto the ring branch is worse: the branch stops
// being a commit on main's history, so the next promotion is no longer a
// fast-forward and every device in the ring wedges instead of one.
//
// WHAT THIS DOES. The ring branch is fast-forwarded to the commit that enrolled
// the device, and that same commit is what gets installed. The device is then
// exactly at its ring's head - not ahead of it, not missing from it.
//
// THE GUARD. Fast-forwarding also carries whatever else sits on main between
// the old pin and the enrolment, to devices already in that ring, with no soak
// and no health gate. So it is only done when that is provably a no-op: every
// existing member's configuration SHAPE must be identical at both revisions
// (fleet.ClassKeyOf, plus GeneratorGlobalsKey for what the resolver does not
// reach). Identical shapes mean an identical build. Anything else means a real
// change is riding along, and then this refuses and says which ring and how
// many devices - an instruction instead of a Nix attribute error.

import (
	"context"
	"fmt"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// revisionReader reads a repo file at a revision. Satisfied by the git
// adapter; kept as a local interface so the port stays narrow (same shape
// coreversion.go uses).
type revisionReader interface {
	FileAt(ctx context.Context, rev, path string) ([]byte, error)
}

// RingAdvance reports what EnsureRingsContain did for one group, so the caller
// can log it and an operator can see a ring moved without a rollout.
type RingAdvance struct {
	Group string
	From  string
	To    string
}

// EnsureRingsContain fast-forwards the ring branch of every group covering the
// given devices to rev, so those devices exist in the revision they are about
// to be imaged from. Groups with no ring, or whose branch already contains rev,
// are left alone.
//
// Returns the advances made. An error means nothing was moved and the caller
// must not image: the message names the ring to promote first.
func EnsureRingsContain(ctx context.Context, cfg *ConfigService, refs ports.RefUpdater, tags []string, rev string) ([]RingAdvance, error) {
	if cfg == nil || refs == nil || rev == "" || len(tags) == 0 {
		return nil, nil
	}
	f := cfg.Fleet()
	if f == nil {
		return nil, nil
	}
	reader, ok := cfg.repo.(revisionReader)
	if !ok {
		// Without a way to read the old revision the guard cannot run, and a
		// guard that cannot run must not be assumed to pass.
		return nil, nil
	}

	// Which rings cover the new devices, and what each is pinned to now.
	pins := map[string]string{}
	for _, tag := range tags {
		d, ok := f.Devices[tag]
		if !ok {
			continue
		}
		for _, g := range d.Groups {
			for _, anc := range f.GroupAncestry(g) {
				if grp, ok := f.Groups[anc]; ok && grp.Pin != "" && grp.Pin != rev {
					pins[anc] = grp.Pin
				}
			}
		}
	}
	groups := make([]string, 0, len(pins))
	for g := range pins {
		groups = append(groups, g)
	}
	sort.Strings(groups) // deterministic order, so a failure names the same ring twice running

	var out []RingAdvance
	for _, g := range groups {
		pin := pins[g]
		changed, err := membersChangedBetween(ctx, reader, f, g, pin, tags)
		if err != nil {
			return nil, err
		}
		if len(changed) > 0 {
			return nil, fmt.Errorf(
				"ring %q is behind and the commits in between change %d device(s) (%v): promote that ring before imaging a new device into it",
				g, len(changed), changed)
		}
		if _, err := refs.SetRef(ctx, RingBranch(g), rev); err != nil {
			return nil, fmt.Errorf("advance ring %q: %w", g, err)
		}
		if err := refs.PushRef(ctx, RingBranch(g)); err != nil {
			return nil, fmt.Errorf("push ring %q: %w", g, err)
		}
		out = append(out, RingAdvance{Group: g, From: pin, To: rev})
	}
	return out, nil
}

// membersChangedBetween returns the tags of devices already in the group whose
// configuration shape differs between the pinned revision and the current one.
// Empty means advancing the ring builds every existing member identically.
//
// The devices being enrolled are excluded: they are new by definition, and
// they are the reason the ring is moving.
func membersChangedBetween(ctx context.Context, reader revisionReader, now *fleet.Fleet, group, pin string, enrolling []string) ([]string, error) {
	raw, err := reader.FileAt(ctx, pin, FleetFile)
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", FleetFile, pin, err)
	}
	old, err := fleet.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s at %s: %w", FleetFile, pin, err)
	}

	// A group's `pin` is rollout bookkeeping, not a build input: the generator
	// reads dev.pin (cohort release) and never groups.<g>.pin. classKey folds
	// group pins in anyway, deliberately erring toward more classes, which is
	// free for the verdict memo and wrong here - the pin commit ALWAYS sits
	// between the old pin and an enrolment, so every enrolment would look like
	// a change and every ring would refuse. Compare with group pins levelled.
	old = withoutGroupPins(old)
	now = withoutGroupPins(now)

	skip := map[string]bool{}
	for _, t := range enrolling {
		skip[t] = true
	}

	// The fields outside the resolver's reach (chiefly the rollout plan, which
	// decides a device's comin branch) are not in any class key, so they are
	// compared once for the whole document.
	globalsDiffer := old.GeneratorGlobalsKey() != now.GeneratorGlobalsKey()

	var changed []string
	for tag := range old.Devices {
		if skip[tag] {
			continue
		}
		inGroup := false
		for _, g := range old.Devices[tag].Groups {
			for _, anc := range old.GroupAncestry(g) {
				if anc == group {
					inGroup = true
				}
			}
		}
		if !inGroup {
			continue
		}
		if globalsDiffer || old.ClassKeyOf(tag) != now.ClassKeyOf(tag) {
			changed = append(changed, tag)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// withoutGroupPins returns a shallow copy with every group's Pin cleared, so
// two revisions can be compared on what actually builds. Device pins are left
// alone: those DO steer the generator (ringBranchFor releases a device onto
// its ring branch when its pin equals the ring group).
//
// The copy is shallow by design - only the Groups map is rebuilt, and only its
// Pin field is touched, so nothing the caller holds is mutated.
func withoutGroupPins(f *fleet.Fleet) *fleet.Fleet {
	if f == nil || len(f.Groups) == 0 {
		return f
	}
	out := *f
	out.Groups = make(map[string]fleet.Group, len(f.Groups))
	for name, g := range f.Groups {
		g.Pin = ""
		out.Groups[name] = g
	}
	return &out
}
