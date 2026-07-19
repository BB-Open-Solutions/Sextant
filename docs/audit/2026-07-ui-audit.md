# Console UI/UX audit — July 2026

Scope: every template under `internal/http/web/templates/` plus both
catalog locales, audited against the product goal "Intune functionality,
consumer-grade friendliness" for a municipal IT admin who is not a Nix or
git expert. Already-tracked items (rollout confirmation flow, expedited
hint, governance-weakening confirm, wave gates on the monitor, human
setting labels, dependent-field greying) are excluded.

## Systemic patterns

1. **Leaky localization.** The most destructive confirmations (wipe,
   retire, remove, abandon, remove-group) are hardcoded English and bypass
   the catalog entirely; stray words ("Release", raw `org` / `group:`
   option values) do the same. The Dutch catalog is also internally
   inconsistent: "device" vs "apparaat", three different words for soak
   ("inwerktijd", "rijping", "soaken"), halted-by-gate vs user-cancelled
   collapsed into near-identical wording.
2. **Git/Nix/MDM jargon on daily surfaces.** HEAD, branch, merge, gate,
   revision hashes, "rings" (third synonym next to wave/golf), straggler,
   provenance, nixos-facter, CMDB — and an empty state that tells the
   admin to run `nix eval .#catalog --json`, which the target user cannot
   do. Exactly the vocabulary the consumer-grade goal must hide.
3. **Meaning hidden in title= and icons.** Utilisation metrics,
   enforce/lock, manual-gate flags and pin status are icon-only with
   hover-only text; most icon-only buttons lack aria-label. Governance
   toggles live on two pages with different wording posting to the same
   endpoint.

## Findings

Format: location | severity | category | problem → fix.

 1. device.html:301,306,335 | high | consistency | Destructive confirms
    (retire/remove/WIPE) hardcoded English, untranslated in NL → move to
    catalog with nl strings.
 2. groups.html:96, changes.html:58 | high | consistency | More hardcoded
    English confirms ("Remove group?", "Abandon?") → catalog keys.
 3. groups.html:65 + catalog.go follows_head keys | high | jargon | "follows
    HEAD" → "follows the latest configuration" / "volgt de nieuwste
    configuratie".
 4. catalog.go changes.intro | high | jargon | Intro stacks git terms
    (stage/branch/gate/merge/commit) → rewrite as propose → check → approve.
 5. catalog.go async.pending_body, async.failed_title | high | jargon |
    "nix gate is still evaluating", "refused by the gate" → "validation is
    still running" / "rejected by the validation check".
 6. catalog.go no_plan_hint, configure_rings_first | high | consistency |
    "rings" as third synonym for wave/golf → standardize on wave/golf.
 7. catalog.go NL soak strings | high | consistency | Three Dutch words for
    soak → pick one term fleet-wide.
 8. access.html:70-78 vs org_updates.html:91-104 | high | consistency |
    Same governance toggles on two pages, different labels, same endpoint →
    one location or shared keys.
 9. settings.html:39-42 | high | dead-end | Empty state tells admin to run
    a nix CLI command → plain guidance, hide the command.
10. overview.html:114,123 | high | raw-value | "Revision" column with git
    hash on the daily overview → show release name, hash on hover.
11. devices.html:55 | high | raw-value | Raw revision inline on the device
    list → release/version name.
12. devices.html:58 | med | a11y | CPU/RAM/Disk only distinguishable via
    near-identical icons with title= → visible letters or headers +
    aria-label.
13. devices.html:43 | med | hidden-info | Icon-only utilisation column
    header → visible/off-screen text label.
14. device.html:271,274 | med | jargon | "Hardware facts (nixos-facter)" →
    drop tool name.
15. catalog.go assigned_user_hint | med | jargon | "(CMDB)" acronym → "for
    your inventory".
16. device.html:79,84 | med | jargon | "Provenance" header + raw
    `group:foo` sources → "Set by" + humanized source.
17. changes.html:34 | med | raw-value | Raw status enum shown untranslated
    → reuse translated pipeline status labels.
18. changes.html:48, access.html:61, policies.html:79 | med | raw-value |
    Scope pickers show raw `org` / `group:x` values → human labels.
19. device.html:298-308 | med | feedback | Retire/Remove styled identical
    to safe actions → distinct destructive styling.
20. device.html:204,208 | med | consistency | Single-select group control
    vs plural "groups" everywhere → multi-select or singular naming.
21. settings.html:54-59 vs :167 | med | consistency | Two save models on
    one page (per-row apps save + page save), per-row is silent → unify or
    add feedback.
22. org_updates.html | med | friction | Four disjoint save forms on the
    "set once" page → clearer sections + per-section feedback.
23. catalog.go stage_binding/stage_button | med | jargon | "Stage" (git
    vocabulary) in EN → align to NL's "klaarzetten" (prepare/queue).
24. updates.html:31, rollout.html:31 | med | raw-value | Bare short hash
    next to release name → drop or move to technical-details hover.
25. overview.html:124 | med | raw-value | Raw Phase enum in activity feed →
    translated labels.
26. org_updates.html:51 | med | hidden-info | Manual-gate wave flagged only
    by hover icon in plan preview → visible "manual sign-off" chip.
27. groups.html:37 | med | jargon | "idP:" casing + raw IdP-group inline →
    consistent "IdP" + clearer label.
28. catalog.go NL locale | high | consistency | "device(s)" vs
    "apparaat/apparaten" mixed → one Dutch term fleet-wide.
29. catalog.go updates.state_* NL | med | consistency | Halted-by-gate vs
    cancelled nearly identical in Dutch → differentiate ("geblokkeerd door
    controle" vs "geannuleerd").
30. compliance.html:64 | low | rendering | colspan=4 on a 5-column table →
    colspan 5.
31. devices/enroll/access/device icon buttons | med | a11y | title= without
    aria-label on destructive icon buttons → add aria-label everywhere
    (policies.html:88 is the good example).
32. settings.html:149 | med | hidden-info | Enforce/lock control explained
    only in title= and page intro → short visible caption.
33. profile.html:36 | low | jargon | "Subject" + raw OIDC subject → "Account
    ID".
34. rollout.html:88 + stragglers keys | med | jargon | "stragglers" /
    "achterblijvers" behind a details-fold, no inline next action →
    "devices not yet updated" + per-device action link.
35. enroll.html:30 | med | dead-end | Non-owners told to register a station
    they cannot register → role-aware message ("ask an owner").
36. updates.html:30,51, rollout.html:30 | low | consistency | "Release"
    hardcoded in markup → catalog key.
37. catalog.go overlays.* | med | jargon | Overlays page is dense Nix →
    plain-language lead, Nix detail secondary (rare page, lower weight).
38. catalog.go enroll keys | low | consistency | Enroll/imaging/Dispatch:
    three verbs for one flow → one action verb.
39. compliance.html:57 | low | a11y | Leftover empty style attr; warning
    tag has no distinct color → remove attr, distinct warning class.
40. device.html:319-323 | med | raw-value | Armed remote-action panel
    prints raw intent/ack values → translated phrases.
