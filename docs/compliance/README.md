# Compliance

Block A of the 1.0 gate: the part that is a legal obligation rather than an
engineering choice, and the part that had nothing in this repository until
2026-08-07.

| Document | What it is | Status |
|---|---|---|
| [accessibility-audit.md](accessibility-audit.md) | WCAG 2.2 AA / EN 301 549, structural measurement of every template | 4 findings, 2 fixed the same day; the manual round is untouched |
| [processing-register.md](processing-register.md) | GDPR art. 30 inventory, measured against production | 10 processings; 1 retention window exists out of 10 |
| [iso27002-mapping.md](iso27002-mapping.md) | What the product covers and what the customer must do | draft; control numbers are ISO 27002:2022, NOT verified against the BIO |

## What these are not

None of them is signed off, and none claims conformance. They are the
factual base a DPO, an auditor and an accessibility tester need in order to
start - written by the people who know where the data actually is, with the
measurement dates in them so a reader can tell how stale they have become.

Three things still have no document at all, and all three need somebody who
is not an engineer:

1. **A DPIA.** The processing register is its input, not its substitute. The
   thing that makes it more than a formality is P8: diagnostics bundles carry
   journal fragments from a staff member's machine.
2. **A processing agreement** between the municipality and whoever operates
   the console.
3. **A published accessibility statement.** It comes last and quotes
   measurements. A statement claiming more than has been tested is a false
   declaration; one admitting partial conformance is normal and expected.

## The standing rule here

Every claim in these documents carries where it was measured and when. A
compliance document that asserts without evidence is the same failure as
`1.0-fit-gap.md` sitting two weeks stale while everybody trusted it - except
that this class of document gets quoted in tenders, where being wrong is
more expensive.

Where a check can be a test, it is one: the accessibility counts live as
ceilings in `internal/http/web/a11y_test.go` and cannot rise.
