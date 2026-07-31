# Contributing to Sextant

Sextant shares its contribution standards with its sibling repository
DAWO-NixOS; the two evolve together.

## Before you push

```
git config core.hooksPath scripts/git-hooks
```

That installs a pre-push hook running `just ci` - the same set the runner runs.
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

Common types: `feat`, `fix`, `docs`, `refactor`, `chore`, `test`.

## Engineering bar
- Spec/ADR before a capability; pure domain with tests; effects behind
  ports; UI is a client of /api/v1 (see docs/capabilities.md).
- `just ci` green before any merge. It mirrors the Forgejo workflow exactly:
  fmt, vet, lint, race tests, the 70% logic-layer coverage floor, build,
  `nix build .#sextant`, the catalog drift guard and the Rust agent checks
  (fmt/clippy/test). A narrower local bar is not the bar.
- Files stay small and single-purpose; edge cases and error paths are
  handled and tested, not assumed.

## Build and test
See the "Build and test" section in `README.md`,
or run `just ci`.
