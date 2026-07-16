# Approval flows

How changes and updates are reviewed before they take effect is configurable
per organisation, under **Access -> Approval flows** (owner). The same three
toggles also live on the [Updates board](../operators/updates.md)'s
governance panel (Step 3) - they are the same setting, shown in both places:

- **Four-eyes** - a change may not be merged by its own author.
- **Require change-request** - configuration edits must go through a
  reviewed change, never a direct commit. When this is on, saving from the
  Configuration editor does not fail - it transparently stages the same
  edits as a fresh change and sends you to the Updates board to see it
  through review.
- **Require test wave** - a rollout must have a gated test wave first; an
  owner can skip it per rollout, and the skip is logged.

Every configuration change is a git commit that passes the Nix gate first, so
an edit that would not build never reaches the fleet.

## Troubleshooting

**Saving settings redirects to the Updates board instead of just saving.**
Expected when **Require change-request** is on - the edits were not lost,
they were staged as a new change. Continue the review from there (diff,
submit, approve).

**A change cannot be merged even though it is Ready.**
If **Four-eyes** is on, the merge button is unavailable to the change's own
author - have a different reviewer approve it.
