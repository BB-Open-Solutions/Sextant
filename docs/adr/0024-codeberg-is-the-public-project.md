# 0024 - Codeberg is run as a public project, forgejo is the workshop

## Status

Accepted 2026-08-10 (Bram). Narrows ADR 0016's "where the code lives" and the
mirror decision of 6 August: those settled *which* forges exist. This settles
**how each is conducted**, which is a different question and the one that
decides whether anybody outside BB Open ever contributes.

## Context

Three forges carry the same history and two of them are ours in every sense.
`forgejo.bb-open.com` is where the work happens: CI runs there, Flux reads
from it, and it is the place a half-finished branch, a scratch tag or a
one-word commit does no harm. `code.overheid.nl` is canonical and is where
the code is published as Dutch government open source.

Codeberg is different in kind rather than degree. It is where somebody who
has never heard of us arrives, and the first thing they see is not the code -
it is whether the project looks like one they could join. A repository with no
milestones, no labels, an empty issue box and releases that are bare tags
reads as a code dump with a licence on it, whatever the code is worth.

The failure this prevents is specific and cheap to fall into: **treating the
public forge as a mirror to push to rather than a project to run.** A mirror
needs nothing. A project needs the parts that make somebody's first hour
productive, and those parts have to exist before the first visitor rather than
after.

## Decision

**Codeberg is conducted as though the community already exists.** Not as
aspiration - as the standard applied from the first day, before there is
anybody to see it.

Concretely, and these are obligations rather than intentions:

- **Issues** carry templates and labels. A newcomer describing a problem is
  handed a shape to fill in, not a blank box.
- **Milestones** exist per release and match `docs/roadmap.md`. An outsider
  can see what 1.1 is without reading a 500-line document, and can see whether
  their problem is already scheduled.
- **Releases** carry notes written for a reader, not a tag with the version as
  its own message. `docs/release-notes/1.0.0.md` is the shape.
- **The roadmap is public and says what forces each item**, so somebody can
  argue with the priority rather than guess it.
- **Nothing lands there half-finished.** A draft belongs on forgejo until it
  is a thing somebody could read and act on.

**`forgejo.bb-open.com` keeps no such obligation.** It is the workshop: scratch
branches, force-pushed ring refs, work in progress. That asymmetry is the point
of writing this down - the standard is not "be tidy everywhere", which nobody
sustains, it is "the public project has a floor and the workshop does not".

## Amended the same day: Codeberg is the source of truth for the project

Bram, a few hours after accepting the above: *"alles inrichten alsof codeberg
de single source of truth is. forgejo.bb-open.com gaat steeds minder
belangrijk worden voor dit project."*

That sharpens the decision rather than replacing it, and the original wording
above is left standing so the shift is visible instead of edited away.

**The project lives on Codeberg.** Issues, milestones, releases and the
roadmap are there, and that is where new work is filed. `forgejo.bb-open.com`
recedes to what it still uniquely does: run CI and serve Flux. It stops being
the place the project is tracked.

Two things this does *not* decide, and neither should be assumed from it:

- **The git source of truth.** Today CI, the release workflow and Flux all
  read from forgejo, and moving that is infrastructure work rather than a
  policy change. Until it moves, forgejo remains where the code is pushed
  first; the sentence above is about where the project is *run*.
- **What `code.overheid.nl` is.** It was chosen as canonical because that is
  where the Dutch government's code belongs, which is a publication
  obligation rather than an engineering preference. Nothing here changes it,
  and changing it is not ours to do quietly.

The practical consequence is the older issue numbers. Around twenty are cited
across these documents and they live on forgejo. They are not renumbered:
rewriting them would cost every reader their references to buy tidiness. New
work is filed on Codeberg, and an item with an older number says so.

## Consequences

- Issue and pull-request templates moved from `.github/` to `.forgejo/`,
  which is what Forgejo - and therefore Codeberg - actually reads. They had
  been invisible on the forge we call the front door. They were **moved rather
  than copied**: two sets is two lists to keep in step, and the GitHub
  repository is being retired.
- Milestones and labels do not exist yet on Codeberg and are the next step.
  They are Bram's to create; nothing in this repository can do it.
- A release now has two artefacts rather than one: the tag, and notes a
  non-engineer can read. `docs/releasing.md` gains that step when 1.0.0 is
  cut.
- This ADR is the thing to cite when a shortcut is proposed. "It is only a
  mirror" stops being an argument on the day this is accepted.

## What is honestly not true yet

There is no community. There are no issues from outside, no external
contributors and no discussion. This ADR does not claim otherwise - it commits
to the conditions being in place before anybody arrives, because the reverse
order does not work. Somebody who finds an empty, unlabelled, milestone-less
repository does not come back in three months to check whether it improved.

## Verification

Checkable from outside, and worth actually checking rather than assuming:
opening a new issue on Codeberg offers the templates; the milestone list is
not empty; the newest release has notes longer than its version number. Each
of those is a thing a visitor sees in their first minute.
