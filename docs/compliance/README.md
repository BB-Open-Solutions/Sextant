# Compliance

Block A of the 1.0 gate: the part that is a legal obligation rather than an
engineering choice, and the part that had nothing in this repository until
2026-08-07.

| Document | What it is | Status |
|---|---|---|
| [accessibility-audit.md](accessibility-audit.md) | WCAG 2.2 AA / EN 301 549, structural measurement of every template | 4 findings, 2 fixed the same day; the manual round is untouched |
| [processing-register.md](processing-register.md) | GDPR art. 30 inventory, measured against production | 10 processings; 1 retention window exists out of 10 |
| [iso27002-mapping.md](iso27002-mapping.md) | What the product covers and what the customer must do | draft; control numbers are ISO 27002:2022, NOT verified against the BIO |
| [dpia-draft.md](dpia-draft.md) | DPIA (art. 35), in Dutch | CONCEPT; six risks, each with what is built and what is left |
| [dpa-draft.md](dpa-draft.md) | Processing agreement (art. 28), in Dutch | CONCEPT; not legally reviewed, not signed |
| [accessibility-statement-draft.md](accessibility-statement-draft.md) | Accessibility statement, in Dutch | CONCEPT; must NOT be published until the manual round is done |

## What these are not

None of them is signed off, and none claims conformance. They are the
factual base a DPO, an auditor and an accessibility tester need in order to
start - written by the people who know where the data actually is, with the
measurement dates in them so a reader can tell how stale they have become.

The three legal documents now exist as DRAFTS, written by the people who
know where the data is so that the question to a DPO or a lawyer is "is this
right" rather than "please make this". They are in Dutch, because that is who
reads and signs them.

None is approved. Each carries its measurement date, and the facts in them
decay: whoever picks one up later has to re-measure, and `docs/audit/`
describes how.

Two of them can be finished by review. The third cannot:

**The accessibility statement must not be published yet.** The manual round -
keyboard, screen reader, contrast, reflow to 400% - has not been done and has
no owner. A statement claiming more than has been tested is a false
declaration; one admitting partial conformance is normal and expected. The
draft says "partially compliant" and lists exactly what was not tested, which
is honest but is not the same as having tested it.

## The standing rule here

Every claim in these documents carries where it was measured and when. A
compliance document that asserts without evidence is the same failure as
`1.0-fit-gap.md` sitting two weeks stale while everybody trusted it - except
that this class of document gets quoted in tenders, where being wrong is
more expensive.

Where a check can be a test, it is one: the accessibility counts live as
ceilings in `internal/http/web/a11y_test.go` and cannot rise.
