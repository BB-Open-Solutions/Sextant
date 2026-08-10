# Contributing to Sextant

Sextant shares its contribution standards with its sibling repository
DAWO-NixOS; the two evolve together.

## Where this project lives

Development happens on **code.overheid.nl/MinBZK/DAWO-Sextant**. That is
canonical: it is where the Dutch government's code belongs, and where the
history is authoritative.

**codeberg.org/DAWO/DAWO-Sextant** is the public mirror and the place to take
part today. It is a European non-profit forge rather than a company's
platform, which is the same reasoning that put the canonical copy on
code.overheid.nl. What makes this open source is the licence and the code, not
the address.

There was a GitHub mirror. It is being retired rather than left as a third
address nobody maintains, so do not open anything there.

A pull request on the mirror is welcome and will not be turned away for being
in the wrong place. It travels back like this: a maintainer applies it to
canonical with your authorship intact, canonical is what releases are cut from,
and the mirror follows. You do not have to do anything for that to happen, and
you do not need an account on code.overheid.nl to contribute.

Issues are read on both. Security reports go to neither - see SECURITY.md.

## Before you push

```
git config core.hooksPath scripts/git-hooks
```

That installs two hooks: `commit-msg`, which checks the message against the
commit standards below, and `pre-push`, which runs `just ci` - the same set
the runner runs.
It costs a few minutes locally and saves a red build plus the follow-up commit
that says "fix CI", which is the expensive way to learn the same thing.

`SKIP_CI_HOOK=1 git push` bypasses it. Use that when you are deliberately
pushing something broken for somebody else to look at, not to get past a
failure you have not read.


## Language policy
- **Primary language: English.** All documentation, pull request
  descriptions, code comments, commit messages and technical discussion.
- **Exception**: Dutch is permitted strictly within the issue tracker for
  internal coordination. Documentation may be translated when needed.

## AI policy
- **Disclosure**: when AI assists, disclose it and name the model(s) and
  agent(s) used, e.g. the commit trailer
  `Code AI-assisted (Claude Fable 5); testing, review and integration by a human.`
- **Responsible use**: use AI where it addresses a specific need; plain
  human discussion and coding is otherwise preferred.
- **Human accountability**: AI-generated code remains the developer's
  responsibility. It must be understandable at a human level so
  troubleshooting can be done by mere mortals.

## Workflow
1. **Issue first**: features, bug fixes and enhancements start as an
   issue; discuss high-level requirements there.
2. **Pull requests**: create the PR once the issue is defined; prefix
   `WIP:` while in progress; assign a reviewer when ready.
3. **Linkage**: every PR references its issue (`Ref: #123`).
4. **Code-only discussions** in PRs; structural debate goes back to the
   issue.
5. **Label** issues and PRs.

## Commit standards

Conventional Commits: `<type>(optional scope): <description>`

Example: `feat(rollout): halt alerting on failed health gates`

Types: `feat`, `fix`, `docs`, `refactor`, `chore`, `test`, `perf`, `build`,
`ci`, `style`, `revert`. A breaking change puts `!` before the colon and
explains itself in the body.

The subject is at most 72 characters, starts lowercase and does not end in a
full stop. A body is separated from it by a blank line.

**This is checked, in two places.** `scripts/git-hooks/commit-msg` refuses the
message while you are still writing it, and CI refuses the push. The rule had
sat in this file unenforced since the repository opened, and on 2026-08-08
one of the last forty commits followed it - which is what a rule nobody
checks is worth. History before that date is left as it is; the check only
looks at the commits a push carries.

**The subject is the smaller half.** What this repository actually asks for
is a body that says *why*: what was observed, what it cost, and what would
have caught it. `git log` here is the closest thing to a design record, and
it is read by people who were not in the room.

## Engineering bar
- Spec/ADR before a capability; pure domain with tests; effects behind
  ports; UI is a client of /api/v1 (see docs/capabilities.md).
- `just ci` green before any merge. It mirrors the Forgejo workflow exactly:
  fmt, vet, lint, race tests, the 80% logic-layer coverage floor, build,
  `nix build .#sextant`, the catalog drift guard and the Rust agent checks
  (fmt/clippy/test). A narrower local bar is not the bar.
- Files stay small and single-purpose; edge cases and error paths are
  handled and tested, not assumed.

## Build and test
See the "Build and test" section in `README.md`,
or run `just ci`.
