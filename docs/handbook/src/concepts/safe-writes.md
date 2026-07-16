# Safe writes and the Nix gate

Every configuration change - a setting, a policy, an overlay, an integration -
is a write to the git overlay. No write reaches git unless it first **evaluates**:
the change is applied to a candidate `fleet.json`, and the Nix generator is run
over the affected hosts. If the evaluation fails - an unknown option, a wrong
type, a value out of range - the write is rejected and nothing is committed. If
it succeeds, the change commits with the author's identity from their session.

This is the core safety property: the console cannot commit a fleet that does
not build.

## Fail-closed

In production the gate runs **remote**: the console ships no Nix, and delegates
the evaluation to a small Nix-capable gate-runner. Remote means fail-closed - a
write that does not evaluate, or that the runner cannot be reached to evaluate,
is refused. A broken or unreachable gate blocks writes rather than waving them
through.

Three modes exist: `eval` (evaluate in-process, for a Nix-capable console),
`remote` (delegate to the gate-runner, the production posture) and `none` (no
gate, for tests or a console with no flake).

## Where it sits

- A direct setting edit is gated before it commits.
- A change-request is gated when it is opened and again before it merges, so a
  reviewer never approves something that will not build.
- A rollout advances branch refs the gate already validated; it ships evaluated
  revisions, it does not re-open the edit path.

The gate is a type-and-build check, not a policy check. Governance - who may
edit, four-eyes review, a required test wave - sits on top of it, in the
change-request and rollout flows (see [Approval flows](./approvals.md) and
[Ship an update](../operators/updates.md)).

## Troubleshooting

**A write is rejected with a short error.**
That message is a *distilled* line pulled out of the gate's evaluation
trace - usually the actual cause (an unknown option, a wrong type, a value
out of range), not the whole trace. A change-request's failure card, and any
rejected save, carries the full multi-line detail behind a "technical
detail" fold if the short line is not enough to act on.

**Every write is refused, even ones that should evaluate fine.**
In `remote` mode the gate is fail-closed: if the gate-runner cannot be
reached at all, writes are refused rather than committed unvalidated. Check
the gate-runner is up and reachable before assuming the change itself is at
fault.
