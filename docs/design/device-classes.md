# Design: device classes and per-class images

Status: DRAFT - session input (Bram + Claude), 2026-07-17. Generator hooks
(`coreModulesFor`, `extraModulesFor`) are shipped; everything else here is
undecided until this design is agreed.

## The idea

One fleet carries more than one kind of machine: workplace laptops, headless
servers, imaging stations - later tablets and mobile. The device's **class**
(an existing, filterable CMDB field) picks its **image** (core module set) in
the overlay flake:

```nix
coreModulesFor = tag:
  if class == "server" then serverModules else desktopModules;
```

**Class, not group, carries the image.** Class is what the machine *is*
(intrinsic); groups are how the organisation manages it (settings, waves,
RBAC). A server sits in group `zaanstad` next to laptops without forcing the
tree to mirror hardware kinds.

Consequence: class stops being free text and becomes a defined vocabulary
(dropdown; a class without an image mapping cannot build and is refused at
edit time, not at gate time).

## The problem Bram spotted: mixed groups

A group holding both laptops and servers accepts `desktop = plasma` at group
scope. Today (one shared image) the server would genuinely install Plasma -
a broken headless machine. After the image split, the headless image does not
define `dawo.desktop`, so evaluation fails and the gate refuses the WHOLE
change - safe, but one server blocks a legitimate laptop setting.

Both outcomes are wrong.

## Proposed answer: derived applicability

**A setting applies to a device only when the device's image defines the
option.** Not a hand-maintained tag - derived from the images themselves, so
it can never drift:

- The catalog export evaluates per image and unions the entries, each tagged
  with the classes whose image defines it (`classes: ["laptop","desktop"]`;
  absent = universal).
- The resolver (both twins: Go and resolve.nix - parity harness extends to
  this) drops a resolved setting for a host whose class is not in the
  setting's class set. The generator therefore never feeds `desktop` to a
  server host: eval stays green, laptops get Plasma.
- The console shows it: on a mixed-scope editor, a class-bound setting says
  "applies to laptops/desktops (3 of 5 devices here)"; device-page provenance
  shows "not applicable (server)".

Fail-closed stays intact: an option NO image defines is still unknown ->
module eval fails -> gate refuses. Applicability only routes known options to
the machines that have them.

## Edge cases (the session checklist)

1. **Mixed group + DE setting** - the core case: laptops apply, servers skip
   visibly. Covered by applicability.
2. **Enforced setting not applicable to a class** - an org-enforced
   `desktop` is skipped by servers like any other non-applicable setting.
   Security-relevant controls (firewall, ssh, audit) must therefore exist in
   EVERY image - a review rule on image definitions, worth a CI assert
   (shared baseline module both images import).
3. **Re-classing a device (laptop -> server)** - its image, applicable
   settings and shape class all change in one commit; previously-applied
   DE settings become non-applicable (visible in provenance), not errors.
   The equivalence partitioner already keys on class + resolved settings.
4. **Unknown class / class without image** - refused at the console edit
   (vocabulary dropdown) AND by a generator assert (defense in depth for
   direct git edits).
5. **Apps have the same problem** - a flatpak on a headless server is
   nonsense. Same rule: flatpaks apply only to classes whose image carries a
   session/flatpak stack; packages are generally universal. Needs the same
   class tagging on the app surfaces.
6. **Policies + filters** - a policy assigned org-wide with a DE setting hits
   servers via the chain; applicability handles it identically. Filters can
   already target class (`AttrClass`) for the *intentional* narrowing.
7. **Catalog UI** - the settings editor at a scope shows every entry but
   badges class-bound ones; a device-scope editor hides entries not
   applicable to that device's class.
8. **Parity** - the Go resolver and resolve.nix must drop non-applicable
   settings identically; extend the parity harness with mixed-class cases.

## Open questions for the session

- Class vocabulary v1: `laptop`, `desktop`, `server`, `station`? (tablet/
  mobile later; is desktop==laptop one image or two?)
- Does the station image subsume the current bb-open/inspoelstraat repo
  (NUC config finally under Sextant management)?
- Shared security baseline: which controls are mandatory in every image?
- Where does the class->image mapping live: overlay flake only (nix), or
  also declared in fleet.json so the console can validate without nix?

## Build order (after agreement)

1. Overlay bump to the generator with the hooks; define desktop + headless
   images from the shared baseline; `coreModulesFor` by class; station role
   via `extraModulesFor`.
2. Catalog export per image with class tags; regen overlay catalog.
3. Resolver applicability (Go + nix twin + parity cases).
4. Console: class vocabulary dropdown (identity card + enroll), applicability
   badges, provenance "not applicable".
5. Move the NUC to `infra` and onto the station image.
