# 0019 - A standard module set, and a per-tenant overlay that holds only data

## Status

Accepted 2026-08-06 (Bram): "What I really want is to make this a kind of
standard overlay in Sextant, with only the ability to switch the
integrations on and off - mainly because in practice those overlays will
barely be adjusted, if at all."

## Context

"The overlay" names two different things, and the confusion is expensive.

**A per-tenant configuration plane.** `fleet.json`, the age-encrypted
secrets, the ring branches devices follow. One repository per organisation,
by design (ADR 0009): it is the customer's data and their audit trail, and
it cannot be shared.

**A set of `dawo.*` NixOS modules.** `user-rights.nix`,
`elevation-request.nix`, `cache-auth.nix`, `update-window.nix`. These are
product features. Nothing about them is tenant-specific, and no customer is
expected to write or edit one - the whole premise of the console is that a
fleet is configured by settings, not by editing Nix.

Only the first has to be per-tenant. The second is in the tenant repository
because that was the fast path, and the cost of that shortcut became visible
on 2026-08-06, when an operator read the real settings page and reported
thirteen problems in an hour.

**What the split actually looks like today**, measured rather than assumed:

| defined in the tenant overlay | defined in DAWO-NixOS (upstream) |
|---|---|
| `userRights`, `elevationRequests`, `cacheAuth`, `updates.maintenanceWindow`, `openbao` | `diskUnlock`, `desktop.*`, `ssh.options`, `timesync`, `printing` |

The first column we change in an afternoon. The second goes through a fork
and a merge request against MinBZK. Two worlds, two review cadences - and
therefore two quality bars, which is exactly what the settings page shows:
overlay-owned options carry human labels, core-owned ones render as
`desktop.gnome.enable` and `diskUnlock.tpm2.device`.

The evidence is not only cosmetic. `elevationRequests.tag` defaults to
`config.sextant.deviceTag`, which during catalog export evaluates on a host
that does not exist - so `"catalog-export"` shipped to operators as a
default value. `elevationRequests.credentialFile` and
`cacheAuth.credentialFile` are the same fixed path declared twice as an
operator-editable setting, two knobs that must agree and will not say so
when they stop agreeing. Nobody decided any of that; it accumulated because
there was no single owner of the surface.

## Decision

**1. The `dawo.*` module set is product, and Sextant ships it.** The modules
move out of the tenant overlay into a module set Sextant publishes as a flake
input. A tenant overlay keeps what is genuinely theirs: `fleet.json`, the
secrets, `hardware-profiles.json`, and a thin `flake.nix` that imports the
standard set and the core.

The console's promise is that a fleet is configured by settings. A per-tenant
copy of the modules contradicts it by making every customer a potential
maintainer of Nix they never asked to own.

**2. Options that stay upstream get a presentation layer that Sextant
owns.** The catalog export already runs here (`nix/export-catalog.nix`) and
already decides what an operator sees - labels, riskClass, the secret-ref
flag. It gains a data file mapping option name to `label` and `widget`,
merged at export time over whatever the core declares.

The core keeps its types and descriptions; we supply the human name and the
control. No fork, no merge request, no waiting - and when the core later
labels an option properly, our entry is deleted. It is a catch-up mechanism,
not a permanent second source of truth.

**3. Two CI checks keep it honest**, because a mechanism that relies on
somebody remembering is how the current state happened:

- **An exported option with no label fails the build** - neither upstream
  nor in the manifest. A new core option cannot arrive unlabelled.
- **A manifest entry for an option that no longer exists fails too.** This
  is the rename guard: without it, an upstream rename silently drops a label
  and the option quietly reverts to a dotted path. With it, a rename shows
  up as one orphan plus one unlabelled option - two loud failures instead of
  one silent regression.

**4. The expert path is unchanged.** ADR 0014's in-console Nix editor stays
exactly as it is. Standardising the modules is not a claim that nobody will
ever need custom Nix; it is a claim that needing it should be rare and
deliberate rather than the starting condition.

## Consequences

- **A new customer starts with no Nix to write.** Today standing up a tenant
  means copying an overlay including its modules; after this it means a
  repository with a settings document and a secrets directory.
- **The settings surface becomes reviewable in one place.** Labels, widgets
  and which options are operator-facing at all become Sextant's
  responsibility, with tests. Several of the thirteen reports would have been
  caught by the label check alone.
- **Three settings should stop being settings**, and this ADR is what makes
  that a decision rather than an edit: `elevationRequests.credentialFile`,
  `cacheAuth.credentialFile` and `elevationRequests.tag` are mechanism, not
  policy. The precedent is `dawo.fleetSecrets.dir`, which is `internal` with
  a comment explaining that a documented option under `dawo.*` becomes an
  operator-editable console setting and that some things must never be one.
- **Migration is per module and reversible.** A module moves when its options
  are labelled and its types are honest; until then it stays where it is. No
  flag day.
- **bb-open stops being special.** It becomes an ordinary tenant: data, no
  modules. That is also the test of whether this worked.

## What this does not decide

- **Where the standard set lives** - a directory in this repository published
  as a flake output, or its own repository. Both work; the first is simpler
  until somebody outside BB Open wants to consume it without Sextant.
- **Whether the core modules eventually move too.** `ssh`, `timesync`,
  `desktop` and `diskUnlock` are arguably DAWO-NixOS's business rather than
  Sextant's. The presentation layer makes that question non-urgent, which is
  the point of doing it first.

## References

- ADR 0009 (cells: instance per tenant), ADR 0014 (custom overlays in the
  console), ADR 0005 (config is data)
- `nix/export-catalog.nix` - the presentation layer's home
- `docs/design/0005` - cell provisioning scope (manual, reconfirmed
  2026-08-05; a reseller portal is post-1.0, see the roadmap)
