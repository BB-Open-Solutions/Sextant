# 0025 - What a hardware token is scoped to on an admin device

## Status

Accepted 2026-08-10 (Bram). Theme I of `docs/competitive-intake.md` was
waiting on this and is now unblocked for 1.1.

Written because the intake asks for it in as many words: the fleet-wide
choice "is the practical choice, and the argument for it should be written
down rather than defaulted into". A default nobody argued for is a decision
nobody can revisit.

## Context

`pam_u2f` matches a key against a user **and an appId**. The appId is a string
the module sets, and a key handle registered under one appId does not answer
under another. Two shapes are available:

- **Per host** (`pam://$HOSTNAME`, the upstream default). A key must be
  enrolled against every machine it may unlock. A stolen token is useless on
  any device it was not enrolled against, and enrolment does not scale past a
  handful of machines.
- **Per fleet** (`pam://dawo-admin`). One enrolment works on every admin
  device in the organisation. So does one stolen token.

The proposition behind theme I is one token doing four jobs off a single
ceremony: unlock the disk, log in, escalate privilege, authenticate outward.
The appId scope decides how far each of those four reaches.

## The question is not binary, and that is the useful part

Framing it as per-host versus per-fleet hides the real choice, which is **per
use**. The four jobs do not carry the same consequence when the token is in
somebody else's pocket:

| Use | What a stolen token alone achieves | Reasonable scope |
|---|---|---|
| Disk unlock | The disk of the machine it is holding. It cannot travel: the keyslot is on that disk | per host, inherently |
| Login | A session on any admin device, if fleet-scoped | fleet |
| `sudo` | Root on any admin device, if fleet-scoped | **fleet, but not alone** |
| SSH outward | Whatever the resident key reaches - the forge, the cluster, other devices | fleet, with touch |

Disk unlock settles itself: a FIDO2 keyslot is enrolled into a specific
LUKS header, so it is per host whatever the appId says.

Login fleet-scoped is the point of the enrolment ceremony. A thief with the
token still needs its PIN, and if login keeps a password fallback (I2 removes
the password *as the primary*, not as an existence) they need that too.

`sudo` is where fleet scope earns its objection. Root on every admin machine
in the organisation, from one pocket, is a different loss from a session on
one. **I8's rule answers it: a second registered token, not the same one.**
That is not an appId question - it is a policy that the second factor for
privilege escalation must not be the factor that already granted the session.

## Decision

**Fleet-scoped appId (`pam://dawo-admin`) for login and SSH. Disk unlock stays
per host by construction. Privilege escalation requires a second registered
token rather than a wider scope of the first.**

The reasons, in the order they matter:

1. **Per-host does not survive contact with an operator.** An admin with two
   machines and a spare enrols three times per key, and re-enrols on every
   re-image. What people do with a ceremony that painful is keep a password
   fallback and stop using the token, which loses more than the scope gained.
2. **The scope is not the control.** What stops a stolen token is the PIN and
   the touch requirement, both enforced by the key itself and neither
   affected by the appId. Narrowing the appId trades a real usability cost
   for a marginal one.
3. **The consequence is bounded where it matters.** The two things a stolen
   token cannot reach are the disk it was not enrolled against, and root -
   because that needs the second token.

## Consequences

- The key handle is public data, so the user-to-handle mapping lives in the
  fleet document as an ordinary setting (I7). No vault, no secret material,
  and it is visible in review like any other configuration.
- **The registration ceremony has to be self-service.** `pamu2fcfg` must run
  with the key present and the exact appId, and it emits a string. Of the
  three candidates - the provisioning station at imaging, an administrator
  pasting on somebody's behalf, or a console page - only the console page
  scales and only it keeps the private key out of anybody else's hands. That
  page must emit the exact invocation for our appId rather than a generic
  instruction.
- **A shared appId makes the mapping the thing to protect.** Nobody can forge
  a key handle, but somebody who can *add* one to the fleet document has added
  an admin credential. The write path is already gated, reviewed and audited,
  and this is the change that makes those properties load-bearing rather than
  tidy.
- I8's lockout matrix stops being documentation and becomes a requirement:
  four uses, four distinct recovery paths, and no path protected by the thing
  it recovers.

## What would change this

A customer whose admin machines are not one trust domain - a shared service
provider running fleets for several municipalities from one set of laptops.
Then `dawo-admin` spans organisations that should not share a credential, and
the appId belongs per tenant rather than per fleet. Worth noticing now because
the cell model already anticipates that shape (ADR 0009), and because the
appId is a string in a module rather than an architecture.
