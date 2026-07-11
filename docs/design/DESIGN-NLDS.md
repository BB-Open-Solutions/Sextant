# Design foundation: NL Design System (feed this too)

Read alongside DESIGN-PACKET. This sets the design FOUNDATION; a bold
Sextant/BB Open theme sits on top. Where this and the Mintlify system
conflict, this wins on structure and accessibility; the Mintlify palette
may inspire the THEME layer, not the component contract.

## Why NLDS is the foundation for this product

Sextant ships to code.overheid.nl/MinBZK under EUPL, for audited
(semi-)government organisations. The NL Design System (nldesignsystem.nl)
is the shared, open-source design system for exactly that context:

- **Accessibility is law here.** NLDS guidelines target **WCAG 2.2 level
  AA** (plus AAA focus-visibility and pointer-target-size). Government
  software must meet EN 301 549 / WCAG; NLDS bakes it in. Every component
  the designer produces must hold this bar - it is not optional polish.
- **Sovereign + open source.** Dutch/EU, no licence cost, community-
  governed. Aligns with the project's sovereignty default (no US SaaS
  aesthetic locked in).
- **Multi-brand token architecture = the cells model.** NLDS separates
  shared accessible components from per-organisation **themes expressed
  as design tokens**. This maps directly onto Sextant's instance-per-
  tenant cells (ADR 0009): one component library, a theme per customer
  org. Design the token layer so a cell can be re-themed without touching
  components.

## What to take from NLDS

- **Component + pattern semantics** (as documented on nldesignsystem.nl):
  buttons, labels, descriptions, error messages, fieldsets, multi-step
  forms, input validation/error-prevention, links, tables, navigation,
  alerts/confirmations. Sextant's screens map cleanly:
  - the catalog-driven **Settings** form -> NLDS form components
    (fieldset, label, description, error message, validation patterns)
  - **enroll / change / rollout-plan** flows -> multi-step form + review
    + confirmation patterns
  - **destructive actions** (retire/remove/wipe) -> confirmation-page /
    error-prevention patterns (the typed-tag wipe confirm is exactly the
    "prevent catastrophic action" guidance)
  - identifiers, revisions, commit hashes -> monospace, copyable
- **Style guidelines**: typography, colour (contrast-checked), space,
  icons - use NLDS scales as the accessible baseline, theme the values.
- **Design tokens** as the theming mechanism: a base token set + a
  Sextant/BB Open theme that overrides colour/type/space/radius. The
  theme is where distinctiveness lives.

## Tech fit with the current console

The console is Go `html/template` + htmx (server-rendered). NLDS fits two
ways - pick per the designer's preference:

1. **Tokens + CSS only** (lightest): consume the NLDS/Utrecht CSS and
   design tokens, style the existing semantic HTML. No JS framework
   needed; htmx stays. Self-contained and sovereign (tokens are CSS).
2. **Web components**: NLDS's base implementation (the Utrecht component
   library) ships framework-agnostic custom elements. These drop into
   `html/template` output as tags; htmx still drives interactions. A
   future SPA can use the React wrappers instead.

Either keeps the frozen `/api/v1` contract untouched.

## Instruction to add for Claude Design

> Use the **NL Design System** as the accessible foundation: its
> component and form semantics, its style scales, and its **design-token
> theming model**. Target **WCAG 2.2 AA** on every screen (visible focus,
> adequate target sizes, error prevention on destructive actions, proper
> labels/descriptions/error messages on the settings and enrollment
> forms). Then apply a **distinctive Sextant / BB Open theme** as a token
> layer on top - this is where boldness and personality live, within the
> accessible component contract. Structure the theme so a per-tenant cell
> can be re-skinned by swapping tokens only. Draw visual energy from the
> provided palette if useful, but never at the cost of contrast or the
> NLDS component semantics.

## Live references for the designer

- nldesignsystem.nl - components, patterns, style guidelines, tokens
- github.com/nl-design-system - the open-source repos (components +
  design tokens); pull exact package/token names from there rather than
  guessing
- The Utrecht component library is the widely-used base implementation
  to theme from.

## Net recommendation

For a MinBZK / code.overheid.nl product, NLDS is the right FOUNDATION -
accessible, sovereign, government-standard, and a natural fit for the
per-tenant cells. Keep the appetite for a "wild" interface as the THEME,
not the whole system: NLDS gives the rails, the Sextant theme gives the
character.
