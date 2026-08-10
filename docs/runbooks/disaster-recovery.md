# When something bigger than the database is gone

`restore-observed-plane.md` covers losing the database. This covers losing a
whole component, and the useful question is not "how do we rebuild it" but
"what stops working the moment it disappears" - because the answers are
further apart than they look.

Measured against the bb-open fleet on 2026-08-10 by evaluating what a device
is actually configured with, not by reading intentions.

## What depends on what

A device runs five things that reach off the machine:

| unit | reaches | what it does |
|---|---|---|
| `comin` | **the forge** | pulls the ring branch and converges the system |
| `sextant-agent` | the console | check-ins, hardware facts |
| `sextant-actd` | the console | intents: lock, diagnostics, crypto-wipe |
| elevation request | the console | a user asking for a right |
| nix substitution | the binary cache | fetching what the gate already built |

The split that matters: **convergence goes to the forge, everything else goes
to the console.** They fail independently, and one of them is much worse than
people expect.

## The console is gone

**The fleet keeps working.** Devices converge from the forge and the console
never pushes to them, so a console outage does not touch what a machine runs.
This is the pull model paying for itself: there is no command channel to lose.

What stops: check-ins (the observed plane goes stale, then blind), all
intents - so **you cannot wipe a lost laptop while the console is down** -
elevation requests, and imaging new devices.

Rebuilding one needs four things, and only three of them are backed up:

1. **The overlay repository.** You have it; that is the point of config as
   data.
2. **The database.** Restore per `restore-observed-plane.md`, which has been
   walked.
3. **`SEXTANT_SECRET_KEY`.** Not in the backup and never will be. Without it
   the restored rows are ciphertext nobody can open, including every LUKS
   recovery key. **This is the single item with no owner and no escrow.**
4. **The forge credential**, so the new console can write. Re-mint it; it is
   not recovered.

Devices need no reconfiguration if the new console answers on the same URL. If
it does not, `sextant.console.url` is a setting in the fleet document - which
means the change reaches devices through the forge, and therefore works even
while the console is down. That ordering is worth noticing: the config plane
can repair the control plane, not the other way round.

## The forge is gone

This is the bad one, and the shape of it is not what the other runbook implies.

**Measured: a device polls exactly one remote.**
`services.comin.remotes` is set with `mkForce` to a single entry -
`https://forgejo.bb-open.com/bb-open/sextant-overlay-bbopen.git`, branch
`rings/<group>`, every 300 seconds. There is no second remote and no failover.

**Measured: the overlay repository has one remote and no mirror.** The Sextant
source lives on three forges; the repository the fleet actually follows lives
on one. `restore-observed-plane.md` says the config plane is out of scope
because "that is git, it has two remotes and a clone on every device" - that
sentence is true of the Sextant repository and **not** of the overlay.

So if the forge is lost:

- Nothing breaks immediately. Every device keeps running its current
  generation; comin simply fails its poll.
- **Nothing can be changed, on any device, until a forge exists again.** No
  rollout, no setting, no security fix. The fleet is frozen, not broken.
- The history survives on any device that has a clone, and on any workstation
  that has one.

Recovery: stand up a forge at the same URL, push the overlay back from a
clone, and devices resume within a poll interval. The URL matters more than
the host - a new URL means editing `dawo.autoUpdate.options.repoUrl`, which
lives in the repository the devices can no longer reach. Plan to keep the
name.

**The fix, and it is small:** `services.comin.remotes` is a list, so a second
remote pointing at a mirror of the overlay is a configuration change rather
than a feature. It needs the mirror to exist first, which it does not. Until
then this is a single point of failure for the entire fleet's ability to
change, and it should be recorded as an accepted risk rather than discovered
during one.

## The binary cache is gone

Devices substitute from `https://cache.sextant.bb-open.com/cache`, with its
key in `extra-trusted-public-keys` (checked - the key is present, which is not
something to assume: an untrusted substituter is silently ignored rather than
refused).

Losing it does not stop convergence. Devices fall back to `cache.nixos.org`
for anything upstream and **build the rest themselves**. That is the moment
the memory a device needs during `nixos-rebuild` stops being a curiosity, and
it is still unmeasured. A fleet of laptops each building a system closure is a
different machine requirement from a fleet that substitutes one.

## What has been walked, and what has not

Being explicit, because a recovery path nobody has run is an assumption
wearing a runbook's clothes:

| | walked? |
|---|---|
| Database restore | **yes**, 2026-08-07, byte-identical |
| Dependency map above | **yes**, evaluated 2026-08-10 |
| New console against an existing fleet | no |
| Forge rebuilt from a device clone | no |
| Device surviving a console outage | no - reasoned from the pull model |

The two worth walking first are the forge rebuild, because it is the failure
with no mitigation today, and the device-clone recovery, because the whole
forge story rests on a clone having full history and nobody has checked
whether comin's is shallow.
