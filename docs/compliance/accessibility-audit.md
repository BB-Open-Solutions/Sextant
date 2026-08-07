# Accessibility audit - WCAG 2.2 AA / EN 301 549

Status: **first measurement, 2026-08-07.** Static analysis of every console
template. This is not a full audit - it finds what a machine can find, which
is the cheap half. The expensive half (keyboard order, screen-reader
sense, contrast in the real theme, focus visibility) still needs a person.

Why it matters here rather than as a nice-to-have: for Dutch government
software this is a legal obligation, and it comes with a published
accessibility statement (toegankelijkheidsverklaring). `DESIGN-NLDS.md`
picked NL Design System partly for this and then nobody measured.

## What was measured

Every file in `internal/http/web/templates/`. The checks are structural:
does each form field have an accessible name, does each control have text,
is the language declared, is there one `h1` per page.

## Findings

### A1 - Half the form fields have no accessible name (WCAG 3.3.2, 4.1.2)

**Measured: 73 of 146 form fields.** They carry a `name` and often a
`placeholder`, and nothing else:

```html
<input name="description" value="{{.Description}}"
       placeholder="{{$.L.T "common.description"}}">
```

A placeholder is not a label. It disappears the moment somebody types, it is
not reliably announced by screen readers, and it fails contrast requirements
in most themes. A screen-reader user meets this field as "edit text, blank".

Worst pages: `policies.html` (16), `settings.html` (11), `devices.html` (8),
`org_updates.html` (8).

**Severity: high.** This is the single largest accessibility defect in the
console and it is on the pages an operator uses daily. It is also the
cheapest to fix - a `<label for>` or an `aria-label` per field, mechanical
work, no design decisions.

### A2 - Eleven icon-only buttons have no accessible name (WCAG 4.1.2)

**Measured: 11 of 101 buttons** render a Material symbol and no text, with no
`aria-label`. The icon font renders a ligature, so a screen reader announces
either nothing or the ligature name.

Concentrated in `profile.html` (5) and `service_accounts.html` (2).

**Severity: medium.** Fewer instances than A1, and some sit next to a labelled
control, but a delete button that announces as "button" is a genuine hazard.

### A3 - No skip link on most pages (WCAG 2.4.1) - FIXED 2026-08-07

`rollout_confirm.html` and `wizard.html` had one; the shared `layout.html`
did not. Every other page therefore made a keyboard user tab through the
whole sidebar - sixty-odd links - before reaching content.

Fixed in `layout.html`: a skip link that is visually hidden until focused,
and `<main id="content" tabindex="-1">` so the target can actually receive
focus. Both halves are needed; a skip link pointing at an element that
cannot be focused moves the viewport and leaves the focus ring behind.

### A4 - The login page hardcoded `lang="en"` - FIXED 2026-08-07

`layout.html` set `lang="{{.L.Locale}}"` correctly; `login.html` set
`lang="en"` regardless, so a Dutch screen reader pronounced the Dutch login
page with English phonetics. It is the first page every user meets, including
the ones who most need the announcement to be right.

Fixed in the same sitting because it was one line, and left in this document
because an audit that quietly drops what it fixed cannot be read as a record
of what was wrong.

## What is already right

Worth recording, because an audit that lists only failures gets read as
"nothing works":

- **Every image has an `alt`.** No exceptions found.
- **One `h1` per page**, on every page.
- **Server-rendered HTML with no JavaScript requirement** for the core
  flows - the best possible starting position, and the reason the remaining
  work is mechanical rather than architectural.
- The language is declared and follows the configured locale on every page (since A4 was fixed).

## Not measured, and needed before any statement is published

A statement that claims conformance on the strength of this document would
be false. Still open:

1. **Keyboard-only walkthrough** of the five core screens: overview, devices,
   settings, changes, rollout. Tab order, focus visibility, no traps.
2. **Screen reader** (NVDA or VoiceOver) on the same five. This is where
   "technically labelled" and "actually usable" turn out to differ.
3. **Contrast** in both themes, measured rather than eyeballed.
4. **Reflow and zoom** to 400% (WCAG 1.4.10) - the settings tables are the
   risk.
5. **Error identification** (3.3.1): does a rejected form say what was wrong,
   where, and in text rather than by colour alone.
6. **The new 2.2 criteria**: focus not obscured (2.4.11), dragging movements
   (2.5.7), consistent help (3.2.6), redundant entry (3.3.7).

## Order of work

A1 first: it is the biggest, the most mechanical, and it blocks nothing else.
A3 and A4 are one-line fixes and can ride along. A2 after. Then the manual
round, because the manual round is what tells you whether the mechanical work
achieved anything.

The accessibility statement itself comes last and quotes measurements, not
intentions. A statement claiming more than has been tested is worse than a
statement admitting partial conformance - the second is normal and expected,
the first is a false declaration.

## How to re-run this

The structural checks ARE a test: `internal/http/web/a11y_test.go`, run by
`go test` like everything else. The counts above are ceilings in that file,
not observations in this one - they may fall and cannot rise, and the test
says so when the real number drops so the ceiling can follow it down.

That is deliberate. A document describing checks somebody should run ages
exactly the way `1.0-fit-gap.md` did; a failing test does not.
