# 29. NixOS does not speak MDM, and does not need to

Date: 2026-08-24

## Status

Accepted. Extends [ADR 0028](0028-two-control-planes-one-reporting-plane.md).

## Context

ADR 0028 settled that Sextant and Fleet run beside each other. What it did not
answer is where the line falls in an estate that has phones in it, and the
answer turns out to be cleaner than "by function":

| device class | authority |
|---|---|
| iOS, Android | Fleet, over MDM |
| macOS, Windows | Fleet, over MDM |
| NixOS | Sextant: imaging, settings, the gate, rings |
| other Linux | Fleet observes it; nobody has an MDM for it |

No device has two masters, which is ADR 0028's rule applied to a whole estate.
It also makes running two consoles explainable rather than embarrassing:
Sextant cannot manage a phone, because no phone runs Nix, and Fleet cannot
configure a NixOS laptop, because there is no MDM channel into one.

That raises the obvious follow-up. If MDM is how the rest of the estate is
managed, should a NixOS device speak it too?

### What was measured

Against a self-hosted Fleet stood up for this, 2026-08-24:

- **Fleet has no Linux MDM.** The config exposes `windows_enabled_and_configured`,
  `android_enabled_and_configured` and the Apple pair. There is no Linux
  equivalent, and their Linux story is osquery plus disk-encryption escrow
  through fleetd.
- **Windows MDM needs no third party.** A self-signed WSTEP certificate and key
  on the server, and it turns on. (Fleet wants a traditional `RSA PRIVATE KEY`;
  a PKCS#8 key from a plain `openssl req` makes it exit with
  `unexpected block type PRIVATE KEY`, which does not say so.)
- **Apple and Android are account journeys, not engineering.** Fleet hands out a
  ready CSR to sign at Apple, and a Google signup URL for Android Enterprise.
  Both refuse to start until `FLEET_SERVER_PRIVATE_KEY` is set, because that
  key encrypts the stored MDM secrets.
- **Much of the MDM feature set is Premium.** `mdm/disk_encryption/summary`
  answers `402`.
- **A NixOS host already carries the MDM-shaped slots, all empty**: enrollment
  status, disk encryption, an archived recovery key, a managed local account.

## Decision

Sextant does not implement an MDM protocol, and NixOS devices are not enrolled
into Fleet's MDM.

Devices keep pulling their configuration from the overlay. The estate view is
fed with Sextant's own verdicts, not with control.

## Consequences

**What this rules out.** Speaking MS-MDE or Apple MDM from a NixOS device is
buildable and was rejected twice over. It would let Fleet push configuration,
which is precisely what ADR 0023 and ADR 0028 forbid, and it would put the
device in the estate list as something it is not.

**What it makes possible.** The estate view can still be honest. A NixOS device
can carry the same kind of statement an iPhone does - is the disk encrypted, is
the recovery key in escrow, does it meet its controls - through Fleet policies
reading a table the device publishes. Measured working on 2026-08-24: a verdict
Sextant computes shows in Fleet as pass, flips to fail when the verdict
changes, and needs no change to Fleet and no osquery extension.

**And it names something the product should say out loud.** Those MDM-shaped
slots are not gaps. Sextant already does every one of them: imaging from a
station, enforced settings that re-converge after drift, remote intents (lock a
session, collect diagnostics, crypto-wipe a lost machine), and LUKS recovery
keys escrowed with every reveal in the audit log. That is the MDM feature set.
What differs is the direction, and the direction was chosen.

MDM exists because a Mac arrives from a factory and has to be talked into line
afterwards. A NixOS device leaves our imaging line with its configuration
already in it. NixOS is the one platform where MDM is not needed, and saying
that is stronger than claiming we have it.

## Alternatives considered

**Masquerade as a Windows device.** Rejected: it hands configuration authority
to Fleet and misreports the device class. The estate view would be wrong in the
one place people trust it.

**Ask Fleet for a Linux MDM.** No precedent, no protocol, and no device-side
client to speak it. Not a request with a plausible path.

**Fill the MDM slots directly.** They are Premium-gated and fleetd-fed, so this
is not ours to do from the outside. The policy channel reaches the same reader
without either constraint.
