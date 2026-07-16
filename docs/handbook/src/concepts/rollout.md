# How a rollout ships

An update does not reach the whole fleet at once. It promotes through ordered
**waves** (also called rings). Each wave is a group of devices; the next wave
only starts once the current one is converged healthy through its soak
window. A wave can require a manual approval gate - a human checkpoint that
the update was tested.

At scale, a wave's release is also **built before it is promoted**: the
delivery pipeline realises the wave's closures on build workers and pushes
them to a signed binary cache before its branch flips, so devices substitute
(download) a pre-built release instead of each compiling it independently.
While this is happening the wave shows as **Building** on the
[Updates board](../operators/updates.md); once the release lands in the
cache the branch flips and the wave moves to **Deploying**. See
[Scaling to 10,000+ devices](../architecture/scale.md) for the reasoning and
the numbers behind it.

The **Updates** board (Step 3, "the rollout procedure") shows the plan as a
ladder: each wave with its device count (size it small first - a canary -
then wider), soak, health floor and gate. Size a wave by its group and order;
refine each wave with the gates - including an optional **max at once** cap,
so a wave widens cohort by cohort rather than releasing its whole group in
one shot.

An organisation can require a gated test wave before any rollout starts; an
owner may skip it for a specific rollout, and that is logged.

## How "behind" is judged

A device is *behind* when the revision it reports differs from its group's
target pin. For that comparison to work, the deployed revision the agent
reports must be the git revision the config was built from - the same kind of
value the pin holds - not a store label.

This requires **one line in the overlay flake**: set
`system.configurationRevision = self.rev` (or `self.shortRev`) on each host.
The Sextant agent module then publishes it to
`/etc/sextant/configuration-revision`, and the agent reports it on every
check-in. Without that line the field is empty and the agent falls back to
the store label, which can never equal a git-hash pin - so every pinned
device reads as falsely *behind*. A flake with uncommitted changes has no
`self.rev`; commit before building, or the revision is unavailable.

## Troubleshooting

**A wave is stuck on Building.**
Build-before-promote is centralised, one-time compute per distinct
configuration shape - not per device - but it still takes real time on the
build workers. Check the gate-runner/build-worker's health before assuming
the rollout is stuck; a missing or overloaded build worker delays a
promotion, it does not corrupt it.

**A wave never reads Complete even though every device shows online and on
the target revision.**
It may still be inside its soak window, or waiting on a manual approval -
check the wave's status label on the [Updates board](../operators/updates.md)
rather than only the device list.
