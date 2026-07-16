# Ship an update

**Updates** (in the sidebar under Shipping) is the one board for the whole
journey from a proposed configuration change to it running on the fleet:
propose -> review -> ready -> rolling out in waves. Changes and Rollout are
drill-downs reached from cards on this board, not separate destinations you
navigate to directly.

## Step 1: review proposed changes

Every configuration edit - a setting, a policy, an integration - is staged as
a **change**: a named, audited unit of work on its own branch. A change moves
through:

- **Draft** - staged edits, not yet validated.
- **Building** - the Nix gate is evaluating the change (see
  [Safe writes and the Nix gate](../concepts/safe-writes.md)).
- **Ready** - the gate passed; it can be reviewed and merged.
- **Failed** - the gate rejected it; the distilled error is shown inline
  (see Troubleshooting below).

Open a change directly from the Updates board (**Change ID** + **title**,
then *Open change*), or let one open automatically: if your organisation
requires change requests (**Access -> Approval flows**), saving settings from
the Configuration editor stages the same edits as a fresh change instead of
committing straight to git, and lands you back here.

For a **Ready** change: *View diff* shows exactly what will change on disk
per host; *Approve* merges it (blocked on yourself if four-eyes review is
required); *Reject* abandons it. A **Draft** can be *Submitted* (sends it to
the gate) or abandoned.

## Step 2: roll out

Once there is a current revision to ship, this section is either an active
rollout in progress or the button to start one against the latest merged
revision.

Starting a rollout kicks off the wave plan from Step 3 (below) against the
current revision. Each wave shows a live status label as it progresses:

| Label | Meaning |
|---|---|
| Queued | Not yet reached |
| **Building** | Build-before-promote: the wave's release is being realised into the signed binary cache before its branch flips (see [Scaling to 10,000+ devices](../architecture/scale.md)) |
| Deploying | The ring branch has flipped; devices are pulling and converging |
| Soaking | Converged healthy; waiting out the wave's soak window |
| Awaiting approval | Soaked, but the wave requires a manual sign-off before the next one starts |
| Complete | Fully promoted, next wave underway or finished |

An owner can **approve** an awaiting wave to let the rollout proceed, or
**cancel** the whole run.

## Step 3: the rollout procedure

Two panels, both owner reach:

**Wave plan.** Each wave (a "ring") pins one device group plus its promotion
gates: a name, the group, a **soak** window (minutes healthy on target before
the next wave may start), a **minimum healthy %** (defaults to 100 - every
device healthy), an optional **max at once** (a count-capped canary that
widens cohort by cohort instead of releasing the whole group at once), and
whether the wave **requires approval** (a human checkpoint - the enterprise
"test sign-off" step). Size a wave small first (a canary), then wider; tune
the progression by ordering waves and sizing their groups.

**Governance.** Three checkboxes, organisation-wide (mirrored on
**Access -> Approval flows**):

- **Require change request** - configuration edits must go through a
  reviewed change (this board's Step 1), never a direct commit.
- **Require four-eyes** - a change may not be merged by its own author.
- **Require test wave** - a rollout must have a gated (manual-approval) wave
  before it starts; an owner can explicitly skip this per rollout, and the
  skip is logged.

See [How a rollout ships](../concepts/rollout.md) and
[Approval flows](../concepts/approvals.md) for the concepts behind both
panels.

## Troubleshooting

**A change is stuck in Failed.**
The card shows the distilled error - the actionable line pulled out of the
gate's evaluation trace (e.g. *"device X: unknown hardware profile 'Y'"*).
Fix the underlying edit and resubmit; if the message is not enough to act on,
the merge/submit response (and the gate-runner's own logs) carry the full
multi-line trace behind a "technical detail" fold.

**A wave sits on "Building" for a long time.**
Build-before-promote realises the wave's whole release into the signed
binary cache before its branch flips - this is centralised, one-time compute
per distinct configuration shape, not per device, but it is real wall-clock
on the build workers. Check the gate-runner/build-worker health before
assuming something is stuck; a missing build worker delays a rollout, it
does not corrupt one.

**A wave never leaves "Deploying" / "Soaking".**
Devices pull on their own schedule (comin), so convergence is not
instantaneous - check the Devices page or [Compliance](./compliance.md) for
the specific machines that have not landed on the target revision yet;
"behind" incidents there point at exactly which ones and since when.

**"Require test wave" blocks starting a rollout.**
Either add a wave with **require approval** checked to the plan, or (if you
are an owner and this run genuinely does not need one) use the explicit skip
offered on the start form - it is logged either way.
