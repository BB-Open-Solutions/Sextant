// Package incident is the pure compliance engine: given a snapshot of what
// each device is observed to run versus what it is supposed to run, it derives
// the action items an operator must pick up. It is incident-driven - the
// output is a list of problems with a suggested action, not a raw inventory.
// No I/O; the app layer assembles Observations and filters incidents by the
// viewer's scope.
package incident

import (
	"fmt"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

// Kind classifies an incident so the console can icon and group it.
type Kind string

// The incident kinds the compliance engine emits.
const (
	Behind      Kind = "behind"       // running a different revision than its target
	Offline     Kind = "offline"      // stopped checking in
	NeverSeen   Kind = "never-seen"   // enrolled but never reported
	Errored     Kind = "errored"      // reported a build/apply error
	WipeFailed  Kind = "wipe-failed"  // a crypto-wipe did not complete
	WipeRefused Kind = "wipe-refused" // a device declined a wipe intent
	// RolloutStalled is fleet-level: a wave was promoted and never converged.
	RolloutStalled Kind = "rollout-stalled"
	// UnknownConfig marks a device on a revision the config repo cannot place
	// in its own history - it follows a source other than its ring branch.
	UnknownConfig Kind = "unknown-config"

	// CoreOutdated: the device runs an older DAWO core than its target pins.
	// Distinct from Behind on purpose. A configuration lag is an edit that has
	// not arrived yet - a warning. A core lag is an older KERNEL, older
	// hardening and older packages, and once it has persisted it is a real
	// issue: the fleet decided to move and this machine did not.
	CoreOutdated Kind = "core-outdated"

	// PolicyCondition: a policy assigned to this device requires something of
	// its observed state that the device does not meet. Distinct from Behind,
	// which is a config the fleet will converge on by itself: nothing converges
	// a full disk, so this needs a human.
	PolicyCondition Kind = "policy-condition"
)

// Severity orders incidents; higher is more urgent.
type Severity int

// The severity levels, low to high.
const (
	Info Severity = iota
	Warning
	Critical
)

// CoreGrace is how long a device may lag the fleet's core before the lag stops
// being a rollout in progress and becomes an issue. Generous on purpose: a
// laptop can be shut for a week and that is not a fault. What it is not is
// indefinite - an unpatched kernel does not become acceptable by persisting.
const CoreGrace = 14 * 24 * time.Hour

// humanDays renders a lag the way an operator would say it.
func humanDays(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days <= 1 {
		return "a day"
	}
	return fmt.Sprintf("%d days", days)
}

// Observation is one device's compliance-relevant state, assembled by the app
// layer from the observed plane (deployed revision, last check-in) and the
// config plane (the target revision its group is pinned to).
type Observation struct {
	Tag      string
	Group    string // primary group; "" means org-level
	Deployed string // revision the device reports running ("" = unknown)
	Target   string // revision it should run ("" = follows HEAD, not judged)
	// DeployedRelease/TargetRelease are the human release numbers of those
	// revisions (commit counts; 0 = unknown). With both known the Behind
	// text can say "release 142, target 145" instead of two opaque shas.
	DeployedRelease int
	TargetRelease   int
	// Head/HeadRelease are the config repo's own tip as this console sees it.
	// They are not judged; they exist so the unknown-config guard can tell a
	// revision the repo does not know from one it has merely not counted yet.
	Head        string
	HeadRelease int
	// DeployedCore/TargetCore are the DAWO core revisions pinned AT those
	// config revisions ("" = unknown, and unknown is never judged). Two config
	// revisions pinning the same core differ in settings only, however far
	// apart they are - which is exactly the difference an operator needs and
	// a commit count cannot express.
	DeployedCore string
	TargetCore   string
	// TargetCorePinned is when the target's core was pinned. The grace period
	// runs from THAT, not from the config change: a device is not late because
	// a setting moved, it is late because it never took a core the fleet
	// adopted a while ago.
	TargetCorePinned time.Time
	Online           bool      // checked in within the online window
	LastSeen         time.Time // zero = never
	Error            string    // device-reported error ("" = none)
	Ack              string    // last remote-action outcome
	// Failed lists the policy conditions this device does not satisfy. A
	// condition cannot be enforced, only checked (ADR 0017), so a failure is a
	// finding rather than something the fleet converges away. The caller
	// evaluates them - it holds the metrics - and passes only DEFINITE
	// failures: a condition on a metric the device never reported is unknown
	// and must not appear here, because an unmeasured device has not broken a
	// rule.
	Failed []ConditionFailure
}

// ConditionFailure is one unsatisfied policy condition on one device.
type ConditionFailure struct {
	Policy string // policy id, for the trail back to the intent
	Name   string // the policy's human name
	Detail string // e.g. "disk.free_percent is 8, requires >= 15"
}

// unknownConfig reports the wrong-source signature: an ONLINE device on a
// revision the config repo cannot place in its own history.
//
// DeployedRelease == 0 alone is NOT that signature. It is equally what a
// perfectly working lookup returns for a revision this console has not
// fetched yet, so on its own the check would cry wolf on every commit made
// outside the console. Three further conditions gate it:
//
//   - TargetRelease and HeadRelease both resolve: the console CAN count
//     releases right now (the repo adapter supports it, the clone is
//     readable). Without this, a console whose lookup is broken would brand
//     the entire fleet in a single sweep.
//   - The revision is not the device's own target: a device sitting exactly
//     on its pin runs what the fleet asked for, and a zero there is a lagging
//     lookup, never a stray device.
//   - The revision is not the repo's HEAD: the console's own tip is
//     legitimate even in the instant before its release number is cached.
//
// What is left is a revision that is neither the pin the console committed,
// nor the tip it holds, nor anywhere in the history it can read - and a
// pinned device can only receive its revision from the ring branch this
// console moves. The one residual window (another replica promoted seconds
// ago and this one has not synced yet) closes itself on the next sync tick.
func (o Observation) unknownConfig() bool {
	return o.Online && o.Deployed != "" && o.DeployedRelease == 0 &&
		o.Target != "" && o.TargetRelease > 0 &&
		o.Head != "" && o.HeadRelease > 0 &&
		o.Deployed != o.Target && o.Deployed != o.Head
}

// RunObservation is the run-level input to the rollout guard: the state of a
// rollout run's CURRENT wave. A stalled run is a fact about the fleet, not
// about one device, so it feeds a sibling detector rather than widening
// Detect's per-device signature.
type RunObservation struct {
	Ring string // wave label ("Canary", "Phase 1")
	// Target is the revision the wave is converging to.
	Target string
	// Stalled is how long the wave has been promoted without converging, per
	// rollout.State.StalledFor. Below rollout.StallWindow nothing is raised.
	Stalled time.Duration
	// OffTarget names the wave's devices not yet reporting the target.
	OffTarget []string
	// Since is when the wave was promoted, for the incident's age.
	Since time.Time
}

// DetectRollout turns a rollout run into fleet-level incidents. It is a
// sibling of Detect, merged in by the app layer, so the per-device detector
// keeps its signature and its callers.
//
// The engine is right to hold a wave that has not converged - that is the
// gate doing its job - but holding it forever without saying so is how a
// device that CANNOT converge (undecryptable secrets, a failed activation)
// reads on the board as "almost done". Past the stall window the wait itself
// becomes the action item.
func DetectRollout(run RunObservation) []Incident {
	if run.Stalled < rollout.StallWindow {
		return nil
	}
	detail := fmt.Sprintf("Promoted %s ago and still not on %s.",
		humanDuration(run.Stalled), short(run.Target))
	if len(run.OffTarget) > 0 {
		detail += fmt.Sprintf(" %d device(s) still off target: %s.",
			len(run.OffTarget), nameSome(run.OffTarget, 3))
	}
	return []Incident{{
		Kind: RolloutStalled, Severity: Warning, Scope: "org",
		Title:  "Rollout stalled on ring " + run.Ring,
		Detail: detail,
		Action: "The devices are not reaching the target. Check the device's activation log " +
			"(a failed activation makes the updater refuse the new generation) and the rollout monitor.",
		Since: run.Since,
	}}
}

// Incident is one action item for one device.
type Incident struct {
	Kind     Kind
	Severity Severity
	Tag      string
	Group    string
	Scope    string // visibility key: "group:<g>" or "org"
	Title    string
	Detail   string
	Action   string
	Since    time.Time
}

// wipe ack outcomes, mirrored from the observed domain to keep this package
// dependency-free.
const (
	ackWipeFailed  = "wipe-failed"
	ackWipeRefused = "wipe-refused"
)

// Detect turns observations into incidents. One device can raise several (an
// offline device that is also behind). Retired devices must be filtered out by
// the caller; every observation here is expected to be a live device.
func Detect(obs []Observation, now time.Time) []Incident {
	var out []Incident
	for _, o := range obs {
		scope := "org"
		if o.Group != "" {
			scope = "group:" + o.Group
		}
		add := func(k Kind, sev Severity, title, detail, action string, since time.Time) {
			out = append(out, Incident{Kind: k, Severity: sev, Tag: o.Tag, Group: o.Group,
				Scope: scope, Title: title, Detail: detail, Action: action, Since: since})
		}

		switch {
		case o.LastSeen.IsZero() && o.Deployed == "":
			add(NeverSeen, Warning, o.Tag+" has never checked in",
				"Enrolled but no report received yet.",
				"Verify it was imaged and can reach the console.", time.Time{})
		case !o.Online && now.Sub(o.LastSeen) > observed.InactiveWindow:
			// Plain offline is NOT an incident: laptops sleep, travel and
			// take vacations (operator decision 2026-07-29; Intune/FleetDM
			// treat offline as a neutral state). Only prolonged absence
			// escalates - past the window the machine may be lost, broken
			// or shelved, and that IS an action item.
			add(Offline, Warning, o.Tag+" has been offline for over two weeks",
				fmt.Sprintf("Last seen %s.", o.LastSeen.Format("2006-01-02 15:04")),
				"Locate the machine; if it is retired in practice, retire it in the fleet too.", o.LastSeen)
		}

		// A device on a revision this fleet's config never produced is not
		// behind, it is elsewhere: it follows another remote or branch, or
		// somebody built a generation by hand on it. It supersedes the Behind
		// incident below - "the update has not landed, check the rollout" is
		// the wrong instruction for a device that is not listening to the
		// rollout at all, and two contradicting rows for one device is what
		// operators already complained about (ahead/behind, 2026-07-28).
		unknown := o.unknownConfig()
		if unknown {
			add(UnknownConfig, Warning, o.Tag+" runs an unrecognised configuration",
				fmt.Sprintf("Reports revision %s which is not a release of this fleet's config.", short(o.Deployed)),
				"The device is following a source other than its ring branch, or was built by hand. "+
					"Check the updater's remote and branch on the device.", time.Time{})
		}

		// An online device on the wrong revision is drifting from its target.
		// Ahead gets its own title and advice: it means an out-of-band change
		// (or a stale pin), not a lagging update - a headline saying "behind"
		// above an AHEAD detail read as a contradiction (operator feedback,
		// 2026-07-28).
		if !unknown && o.Online && o.Target != "" && o.Deployed != "" && o.Deployed != o.Target {
			detail := fmt.Sprintf("Running %s, target is %s.", short(o.Deployed), short(o.Target))
			title, advice := o.Tag+" is behind",
				"The update has not landed; check the rollout and the device logs."
			sev := Warning
			if o.DeployedRelease > 0 && o.TargetRelease > 0 {
				diff := o.TargetRelease - o.DeployedRelease
				switch {
				case diff > 0:
					detail = fmt.Sprintf("On release %d, target is release %d (%d behind).",
						o.DeployedRelease, o.TargetRelease, diff)
				case diff < 0:
					title = o.Tag + " is ahead of its target"
					detail = fmt.Sprintf("Running %s; its ring is pinned to %s, which is older.",
						short(o.Deployed), short(o.Target))
					advice = "The device runs something the rollout did not stage: an out-of-band change, or a stale pin. Check the pin."
					// Ahead ON THIS FLEET'S OWN LINEAGE is the ordinary state of
					// a freshly imaged device, not a fault. Imaging installs
					// from main, and the engine records each promotion as a
					// commit on main, so a new device is always at least one
					// commit ahead of the ring it is about to join. Calling
					// that "out-of-band" trains an operator to ignore the one
					// message that should mean somebody built a generation by
					// hand.
					if o.HeadRelease > 0 && o.DeployedRelease <= o.HeadRelease {
						title = o.Tag + " is waiting for its ring to catch up"
						detail = fmt.Sprintf(
							"Running %s, which is this fleet's own configuration but newer than the %s its ring is pinned to. A freshly imaged device starts here.",
							short(o.Deployed), short(o.Target))
						advice = "Nothing to do: the next promotion moves the ring past it. If it persists across promotions, check whether the rollout is stuck."
						sev = Info
					}
				}
			}
			add(Behind, sev, title, detail, advice, time.Time{})
		}

		// A core the fleet has moved on from, and this device has not. Only
		// raised when BOTH cores are known: an unreadable revision must not
		// become an accusation. Warning while the change is fresh - a rollout
		// takes time and a laptop may simply be shut - and Critical once the
		// grace period has passed, because by then it is not in flight any
		// more, it is stuck.
		if o.DeployedCore != "" && o.TargetCore != "" && o.DeployedCore != o.TargetCore {
			sev := Warning
			detail := "The fleet moved to a newer DAWO core; this device still runs the previous one."
			if !o.TargetCorePinned.IsZero() && now.Sub(o.TargetCorePinned) > CoreGrace {
				sev = Critical
				detail = fmt.Sprintf(
					"The fleet moved to a newer DAWO core %s ago and this device never took it: it is running an older kernel, older hardening and older packages.",
					humanDays(now.Sub(o.TargetCorePinned)))
			}
			add(CoreOutdated, sev, o.Tag+" is running an older DAWO core",
				detail,
				"Check that the device converges: it may be off, or refusing the new generation. The activation log on the device says which.",
				o.TargetCorePinned)
		}

		// One incident per failed condition rather than one per device: they
		// come from different policies, need different answers, and collapsing
		// them would hide the second behind the first.
		for _, c := range o.Failed {
			name := c.Name
			if name == "" {
				name = c.Policy
			}
			add(PolicyCondition, Warning, o.Tag+" does not meet "+name,
				c.Detail,
				"This is a condition, not a setting: the fleet cannot correct it by converging. "+
					"Free the resource on the device, or change the policy if the requirement is wrong.",
				time.Time{})
		}

		if o.Error != "" {
			add(Errored, Critical, o.Tag+" reported an error",
				o.Error, "Open the device and inspect the failure.", time.Time{})
		}

		switch o.Ack {
		case ackWipeFailed:
			add(WipeFailed, Critical, o.Tag+" wipe did not complete",
				"The device attempted a crypto-wipe but did not confirm completion.",
				"Verify the disk key is destroyed before reusing the device.", time.Time{})
		case ackWipeRefused:
			add(WipeRefused, Warning, o.Tag+" refused the wipe",
				"The device declined the wipe intent (unarmed or an interlock blocked it).",
				"Re-arm the device or clear the interlock, then retry.", time.Time{})
		}
	}
	sortBySeverity(out)
	return out
}

// Sort orders incidents most-urgent first, then by kind and tag. Detect and
// DetectRollout each return sorted output; a caller that MERGES the two
// re-sorts the union so the console still reads worst-first.
func Sort(in []Incident) { sortBySeverity(in) }

// humanDuration renders a stall the way an operator says it ("50m", "1h20m")
// rather than Go's default with its trailing zero seconds.
func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%dh%02dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// nameSome lists at most max names and summarises the rest, so a wave with
// forty stragglers still yields a one-line detail.
func nameSome(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:max], ", "), len(names)-max)
}

// short trims a revision to a readable prefix.
func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// sortBySeverity orders incidents most-urgent first, then by kind and tag, so
// the list is deterministic.
func sortBySeverity(in []Incident) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && less(in[j], in[j-1]); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

func less(a, b Incident) bool {
	if a.Severity != b.Severity {
		return a.Severity > b.Severity
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Tag < b.Tag
}
