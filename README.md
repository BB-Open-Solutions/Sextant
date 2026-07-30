# Sextant

Declarative fleet control-plane for NixOS. Config-as-data in git, built by nix,
deployed by comin. Sextant is the human and API surface that edits fleet
configuration safely, proves it builds, stages the rollout, and reports what
each device actually runs. Devices PULL their configuration; the console never
pushes to them. The few things that must reach a machine directly - lock a
session, collect diagnostics, cryptographically erase a lost laptop - travel
as intents the device picks up and a root executor carries out, never as a
remote command channel.

## Where this repository lives

| | |
|---|---|
| **code.overheid.nl/MinBZK/DAWO-Sextant** | Canonical. The code is published here as Dutch government open source. |
| **Codeberg** | Public mirror, and where participation happens: file issues and open pull requests here. |

The canonical repository is not open to the public yet, which is why the
Codeberg mirror exists. Codeberg runs Forgejo, the same software as our own
forge, so a contribution travels back without anybody translating between
tools. It is also EU-hosted and run by a non-profit, which is the same
argument this product makes to the organisations that buy it.

A read-only GitHub mirror may follow, purely because that is where a lot of
people still look first. If it exists, it is a pointer and nothing more -
issues and pull requests belong on Codeberg.

## What it does

**Configuration**
- Settings resolve along organisation → group → device, with locks so a higher
  scope can hold a value that a lower one may not weaken.
- Policies are the layer above: a name and a reason an auditor can read,
  enforcement, and drift that is re-checked rather than written once.
- Policies also carry *conditions* about a device's observed state - free disk
  space, how long since it checked in - which cannot be enforced, only checked.
  A failure becomes a finding that says so, rather than pretending the fleet
  will converge it away. A device that reports no measurement is never accused
  of breaking one.
- Every `dawo.*` option the overlay publishes appears in the console by itself.
  There is no second list to keep in step.

**Change and rollout**
- A change is submitted, built by a gate before anyone can merge it, and
  approved - optionally by a second pair of eyes.
- Rollouts run in rings: soak times, health thresholds, device caps, pins and
  optional auto-flow.
- A wave that stops making progress becomes an action item that names the
  devices holding it up, instead of waiting silently forever.
- Changes marked high-risk require an explicit extra confirmation.

**Devices**
- Imaging from a provisioning station, installing the revision that device's
  ring is pinned to - not whatever `main` happens to be at that moment.
- Hardware profiles carry the disk layout and the imaging notes.
- Remote intents rather than remote control: lock a session, collect
  diagnostics, or crypto-wipe a lost machine. A wipe needs the device to be
  armed, and reports back when it refuses or does not complete.
- Secrets with agenix. A newly imaged device's host key is registered as a
  recipient automatically, which is otherwise the classic silent failure.
- Disk-encryption recovery keys escrowed, with every reveal in the audit log.

**Fleet health**
- A board of action items: never checked in, offline, reported an error,
  running an unrecognised configuration, failing a policy condition.
- A configuration that lags is a warning. A *system* that lags becomes a real
  issue once it persists, because those are not the same problem and reporting
  them identically trains people to ignore both.
- Devices are shown as matching or not matching what they should run. Revision
  hashes are there for the operator who asks, not for everyone who looks.

**Integrations, as ordinary fleet settings**
- NetBird mesh, directory login over LDAP/LDAPS with SSSD, Wazuh endpoint
  security, OpenBao, and SMTP for notifications (with a one-click preset for
  Lettermint, an EU mail route).
- Endpoint controls: USB device control with an allowlist, printing, and
  per-capability user rights - so somebody can pick a WiFi network or approve a
  dock without anybody handing out an administrator password.

**Evidence**
- An audit log of who changed what, and when.
- An evidence export, CSV exports of devices and policies, and per-policy
  BIO/ISO control annotations: the auditor's cross-reference from a framework
  to the thing that actually enforces it.
- Access is scoped, so an operator responsible for a few groups sees those
  groups.

## Status: Beta

The rebuild is feature-complete for its first production use and is being
prepared for one. Running it: the fleet this is developed against runs it,
including imaging, rollouts in rings, directory login, endpoint security and
disk-encryption escrow on real hardware.

Beta means the shape is settled and the remaining work is proving it rather
than designing it. Expect the APIs and the fleet document schema to stay put;
expect rough edges in places the first fleet has not exercised yet, and expect
us to say which those are rather than pretend otherwise.

**Help wanted:** developers, testers and maintainers. If you would like to
collaborate, please contact Bram Buijs at **b.buijs@bb-open.com**.

See `docs/adr/` for the decisions and `docs/architecture.md` for the design.
Contributors welcome - see CONTRIBUTING.md.

## Architecture (short)

Hexagonal: pure domain, use-case services, ports, adapters, thin transport.

```
internal/domain    pure model + scope/policy resolution (no I/O)
internal/app       use-case services
internal/ports     interfaces the app depends on
internal/adapters  git, nix, postgres, ldap, oidc, integrations
internal/http      SSR web (html/template, form-POST) and /api/v1 JSON
internal/platform  config, logging, metrics, server lifecycle
```

## Build and test

```
just ci      # fmt-check, vet, lint, test -race, build
just run     # run the server locally
```

Or without just: `go test -race ./... && go build ./...`.

## License

See LICENSE.
