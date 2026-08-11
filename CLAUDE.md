# House rules

Loaded at the start of every session, which is the point: these are the rules
that kept getting rediscovered and then lost again. Three of them had been
written down in `CONTRIBUTING.md` since the repository opened and were
followed by almost nobody, so the standing lesson here is not "write it
down", it is:

**A rule nobody checks is a preference.** Where a rule can be enforced by a
script, it is, and this file says where. Where it cannot, it is stated plainly
enough that breaking it is a decision rather than a drift.

Evidence, in case that reads as theory:

- Conventional Commits were required from day one. On 2026-08-08, one of the
  last forty commits followed them. Now `scripts/check-commit-message.sh`
  refuses the message.
- The coverage floor sat in CI from 16 July to 1 August without running: a
  `$$` expanded to a PID and the check printed a number nobody could fail.
- "Issue first, then PR" is in `CONTRIBUTING.md`. On 2026-08-11 ten commits
  went straight to `main` in one afternoon.

## Workflow

**Issue, then branch, then pull request, then merge.** Nothing goes straight
to `main`. The PR body carries `Ref: #<n>`, and both the issue and the PR get
labels.

Issues live on Codeberg, which is where the project is run (ADR 0024). They
are also data: `docs/project/issues.json`, applied by
`scripts/codeberg-project-setup.sh`. Writing one there means it gets the same
review as code and can be recreated on any Forgejo-family forge.

`forgejo.bb-open.com` is the workshop and keeps no such obligation. Scratch
branches and force-pushed ring refs belong there.

**Push to both remotes.** Flux reads forgejo; a half-push serves the old
version and says nothing.

**Never `git add -A`.** Stage by name. On 2026-08-11 an `-A` swept about 700
lines of a parallel session into three unrelated commits, and published
history cannot be rewritten.

## Writing

Applies to commits, comments, documentation, issues and pull requests alike.

**English**, with three exceptions recorded in the language ADR. That includes
quoting Bram: translate rather than paste.

**No em dashes.** Use a comma, a colon, a full stop, or two sentences.
Enforced for commit messages by `scripts/check-commit-message.sh` and for
changed lines by `scripts/check-house-style.sh`.

**No decorative punctuation.** No exclamation marks, no ellipses standing in
for thought, no bolded intensifiers. Bold is for the one claim a reader must
not miss, not for emphasis in general.

**Say what forces the decision, not that a decision is important.** "The
console holds the target revision and the device does not" beats "this is a
critical distinction".

**No AI slop.** No summaries of what the reader just read, no "in conclusion",
no restating the task back. If a paragraph could be deleted without losing a
fact, delete it.

**Comments explain why, never what.** The code already says what. A comment
that survives review names the alternative that was rejected, the measurement
that settled it, or the failure it prevents.

**Measured claims carry their measurement.** Dates, numbers, and the command
that produced them. "Measured 2026-08-11: the flag was still 1 half an hour
after the revert" is worth more than "the flag is sticky".

## Commits

Conventional Commits, subject at most 72 characters, lowercase start, no full
stop. Written to a file and passed with `git commit -F`, never inline: inline
messages end up short, and shell quoting mangles them.

The body says why. A commit that changes behaviour says what was measured.

## Tests

**A test is accepted when a mutation kills it.** Turn the guard around, run
the suite, and record the number of failures in the commit body. A test that
survives its own mutation is documentation with a green tick.

An equivalent mutation is a finding, not a gap. Say so in the body rather than
contorting a test to kill something that cannot be killed.

**Measure the path you deploy.** A success message is not proof. This repository
has a standing habit of checking the running system rather than the code that
was supposed to change it, and it has paid for itself repeatedly.

**No silent caps.** If a check bounds its own coverage, it says what it
dropped. A test that quietly stops testing is worse than no test: it reads as
a pass.

## Product

Stays simple and deployable on an RKE2-style cluster, and highly available.
Complexity that only a specialist can operate is a defect in a product a
municipality has to run.
