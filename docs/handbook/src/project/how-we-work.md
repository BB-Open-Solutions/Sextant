# How this project works

Sextant is a DAWO community project. This chapter is the part of the handbook
that is not about running a fleet: who decides, how a change reaches a release,
what we ask of a contributor, and where to send a vulnerability.

It exists because those questions get asked by people evaluating the project,
not just by people writing code, and answering them once in public is cheaper
than answering them per e-mail.

## The short version

| Question | Answer |
|---|---|
| Licence | EUPL 1.2, for the whole product. No paid tier holds a feature back. |
| Where the code lives | `code.overheid.nl/MinBZK/DAWO-Sextant` is canonical; `codeberg.org/DAWO/DAWO-Sextant` is the public mirror and the place to take part. |
| Who steers it | BB Open is the steward: it keeps the roadmap, reviews contributions and cuts releases. Stewardship is a role, not ownership of what you run. |
| Who may run it for others | Anyone. The licence permits hosting, supporting and reselling Sextant without asking us. |
| Language | English for everything that lands in the repository. |
| Security reports | `security@bb-open.com`, acknowledged within three working days. |

## What stewardship means, and what it does not

The steward keeps the roadmap, reviews what comes in, cuts the releases and
answers for the security process. That is work, and somebody has to do it.

It is not a licence to change the deal. The code is EUPL 1.2 and stays that
way; an organisation that wants to run, support or resell Sextant needs
nobody's permission, and a fork is a right rather than a threat. The one thing
that is not covered by the licence is the name: *Sextant* and the mark belong
to BB Open Solutions B.V., so a fork may say it is based on Sextant but may not
present itself as Sextant.

## Decisions are written before they are code

Anything that shapes the product goes into an architecture decision record
before it goes into the codebase. The ADRs live in `docs/adr/` and are numbered
in the order they were taken; they say what was decided, what it rules out, and
what would have to change for the decision to be revisited.

The rule behind it: we would rather argue about a design in writing than
discover the disagreement in review. It also means somebody joining two years
from now can read why the product is shaped the way it is without asking
anyone.

## Contributions

Contributions come in under the EUPL 1.2, like everything else here; you keep
your copyright. The full standard is in `CONTRIBUTING.md`; the parts people
trip over are:

- **An issue first.** Features, fixes and enhancements start as an issue, and
  the pull request references it.
- **Conventional Commits**, checked by a hook while you write and by CI when
  you push. The subject is the smaller half; the body should say *why*.
- **Stage the files the commit is about, by name.** Not `git add -A`. A message
  can pass every check and still describe the wrong contents.
- **`just ci` green before a merge.** It mirrors the CI workflow exactly:
  formatting, vet, lint, race tests, the coverage floor, the Nix build, the
  catalog drift guard and the agent checks.

Pull requests on the mirror are welcome and are not turned away for being in
the wrong place; a maintainer applies them to canonical with your authorship
intact.

## AI assistance is disclosed

Where AI assists, we say so and name the model, with a commit trailer such as
`Code AI-assisted (Claude Fable 5); testing, review and integration by a human.`

The code remains the developer's responsibility either way, and it has to be
understandable by a human who has to troubleshoot it at three in the morning.
That is a stricter bar than "it works".

## Security

Send it to `security@bb-open.com`. You do not need a proof of concept or
certainty — "this looks wrong and here is why" is a useful report.

- Acknowledged within **three working days**. If you hear nothing, chase it;
  assume the mail went astray rather than that you are being ignored.
- An assessment within **ten working days**: whether we can reproduce it, how
  serious we judge it, and roughly when a fix lands.
- **Credit** in the release notes unless you would rather not be named.

We ask for a reasonable window before publication, and we will not use that
window to argue about severity. If we disagree with your assessment we will say
so plainly, and you remain free to publish. The full policy, including what is
in scope and what we consider serious, is in `SECURITY.md`.

## Releases

Releases are cut from canonical, tagged, and shipped with notes that name what
changed and who reported what. Supported releases get security backports.
Organisations with a support agreement hear about a security release directly;
everyone else reads the advisory in the repository.
