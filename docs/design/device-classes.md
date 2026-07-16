# Design: device classes and per-class images

Status: DIRECTION DECIDED (Bram, 2026-07-17) - orthogonal classes with
derived applicability, plus per-group class guardrails. Remaining open
points are at the bottom; build order follows the delivery-process session.

## The idea

One fleet carries more than one kind of machine: workplace laptops, headless
servers, imaging stations - later tablets and mobile. The device's **class**
(an existing, filterable CMDB field) picks its **image** in the overlay
flake via the shipped generator hooks (`coreModulesFor`, `extraModulesFor`).

**Class, not group, carries the image.** Class is what the machine *is*
(intrinsic); groups are how the organisation manages it (settings, waves,
RBAC). The two stay orthogonal: a server may sit in group `zaanstad` next to
laptops without forcing the tree to mirror hardware kinds.

Class becomes a defined vocabulary (dropdown; a class without an image
mapping cannot build and is refused at edit time).

## Why orthogonal: the industry survey

The mixed-scope problem does not come from groups: an org-level
`desktop = plasma` reaches every server too, and org scope is mixed by
definition. Applicability is therefore needed regardless of any group rule -
which makes the group model a free choice, and the industry made it long ago:

| Product | Model |
|---|---|
| Intune | Mixed Entra groups; per-platform settings catalogs; assignment filters on device facts |
| SCCM/MECM | Query collections; deployments carry *requirements* evaluated per device |
| GPO/AD | Mixed OUs; WMI filters decide whether a policy applies |
| Jamf / Workspace ONE | Smart (dynamic) groups on criteria |
| ChromeOS | Strict OU tree - but single-platform, so the question never arises |
| Puppet | Roles & profiles: one role per node, composed from shared profiles |
| FleetDM | Exclusive teams (~tenancy) + dynamic labels (~filters) |

Nobody types the *group*. The convergent pattern is: static organisational
hierarchy + intrinsic class/platform + applicability rules + dynamic filters
for targeting. Two ideas from the survey are adopted outright:

1. **Composition, not disjoint images (Puppet roles/profiles).** Every class
   = the shared security baseline (firewall, ssh, audit, agent - mandatory)
   + class modules (DE stack, server stack, station role). A class can then
   never "skip" a security control structurally - no review rule needed.
   `coreModulesFor = tag: baseline ++ classModules.${classOf tag}`.
2. **Class is set at enrollment (DEP/Android Enterprise pattern).** The
   imaging pipeline stamps the class; changing it later is a deliberate,
   audited act - never drift.

Explicitly NOT adopted: dynamic/smart groups. Membership that shifts because
a fact changed moves a device between security scopes without a decision;
in a git-audited product the tree stays static (every move is a commit) and
the dynamism lives in assignment filters, which already exist.

## Decided: A + C

**A - derived applicability.** A setting applies to a device only when the
device's image defines the option - derived from the images themselves, so
it can never drift:

- Catalog export evaluates per image and unions entries, each tagged with
  the classes whose image defines it (absent = universal).
- Both resolver twins (Go and resolve.nix; parity harness extends to this)
  drop a resolved setting for a host whose class lacks it. The generator
  never feeds `desktop` to a server: eval stays green, laptops get Plasma.
- The console shows it: a mixed-scope editor badges class-bound settings
  ("applies to laptops - 3 of 5 devices here"); device provenance shows
  "not applicable (server)".
- Fail-closed intact: an option NO image defines is still unknown -> module
  eval fails -> gate refuses.

**C - per-group class guardrail.** A group may declare `allowedClasses`
(empty = any). The console refuses adding/moving a device of another class
into it, and refuses narrowing the restriction below existing members;
the generator asserts it too (defense in depth for direct git edits).
This guards *intent* ("my server tree stays servers"); A guards *semantics*.
Both ship in v1 (Bram: A in combination with C).

## Edge cases (session checklist, resolved by the above)

1. Mixed group + DE setting -> laptops apply, servers skip visibly (A).
2. Security baseline on every class -> structural via composition (survey
   idea 1), not a review rule.
3. Re-classing a device -> image, applicable settings and shape class change
   in one audited commit; stale settings become "not applicable", not errors.
   Console warns; guardrail C may refuse if the group restricts classes.
4. Unknown class / class without image -> console dropdown + generator
   assert.
5. Apps: flatpaks apply only to classes whose image carries a session stack;
   packages generally universal. Same class tagging on app surfaces.
6. Org-wide policy with DE settings -> applicability handles it identically;
   filters (`AttrClass`) remain for intentional narrowing.
7. Settings editor at device scope hides non-applicable entries; group/org
   scope badges them.
8. Parity: Go resolver and resolve.nix must drop identically - extend the
   parity harness with mixed-class cases.
9. Waves: a mixed ring updates servers and laptops in one wave. Operator
   choice: put servers in their own groups/waves for maintenance windows -
   guardrail C makes that intent enforceable.

## Open points

- Class vocabulary v1: `laptop`, `desktop`, `server`, `station` - and is
  laptop/desktop one image (workplace) or two?
- Does the station image subsume bb-open/inspoelstraat (NUC config under
  Sextant management)?
- Where does class->image mapping live: overlay flake only, or also declared
  in fleet.json so the console validates without nix?
- Baseline contents: the mandatory control set per organisation.

## Build order

1. Overlay bump to the hooks generator; baseline + class-module composition;
   `coreModulesFor` by class; station role via `extraModulesFor`.
2. Catalog export per image with class tags; regen overlay catalog.
3. Resolver applicability (Go + nix twin + parity cases).
4. Console: class vocabulary dropdown (identity card + enroll wizard, class
   stamped at enrollment), applicability badges, "not applicable"
   provenance.
5. Group `allowedClasses` guardrail (model + console + generator assert).
6. Move the NUC to `infra` on the station image.
