# 0006 — Recommended-settings profiles

Status: build-ready. Domain landed; console flow follows.

## Problem

A new organisation faces an empty settings surface and has to know what a
sensible laptop configuration looks like. Groups do not solve this: a
group mixes hardware kinds, and a "laptop" starting point applied to a
group would also configure its servers. The starting point must also be
adjustable per deployment - DAWO ships opinions, a tenant tunes them.

## Decision

A profile is a curated bundle of recommended settings for one kind of
device, authored as data in the overlay (`profiles.json`, next to
`catalog.json` and `hardware-profiles.json`). The console instantiates a
profile as a REGULAR policy plus a class filter and an org-wide
assignment - no new resolver concept, no schema change beyond one
provenance field. Everything a profile sets stays visible and overridable
through the normal scope chain: a profile is a starting point, never a
lock. Mandatory-style control stays where it already lives (enforced
keys, org scope, comply-or-explain register).

## Shape

`profiles.json` is a list of `{name, label, description, class?,
settings}`. `class` narrows the instantiated assignment via a
`class-<x>` eq-filter; empty means fleet-wide. The DAWO core overlay
ships defaults (laptop, infra, ...); a tenant overlay may replace or
extend the file. Which options exist at all is already the catalog
export's job; profiles only pick values.

## Instantiation (fleet.ApplyProfile)

One mutation through the normal safe-write transaction (gate validates,
audited commit):

- policy id = profile name; `Policy.Profile` records `name@hash` where
  the hash covers class + settings (wording changes don't move it).
- a hand-made policy on that id is refused, never clobbered.
- the `class-<x>` filter is created only when missing - a hand-tuned
  filter of the same name is reused, not overwritten.
- the org assignment is added only when absent; re-apply is therefore
  the drift-repair path: it refreshes settings and provenance and
  nothing else.

## Drift

The console compares each instantiated policy's recorded hash with the
overlay's current profile: same hash = "volgt profiel", different =
"profiel bijgewerkt" with a diff and a one-click re-apply (which is just
ApplyProfile again). Local overrides live at group/device scope and are
untouched by re-apply - that is the point of instantiating into the
normal scope chain.

## Not doing

- A profile layer in the resolver (org -> profile -> group): more moving
  parts for no capability the policy plane doesn't already have.
- A profile editor in the console: profiles are overlay data so they
  version with the configuration they describe and the UI stays clean.
- Validation of profile settings against the catalog at parse time: the
  gate is the validator; a broken profile fails loudly at apply, before
  anything reaches git.
