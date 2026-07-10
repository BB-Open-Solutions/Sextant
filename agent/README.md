# sextant-agent

The device side of Sextant (ADR 0010). Declarative model: comin converges
configuration; the agent only **observes and reports**.

- every beat: `POST /api/checkin` with the deployed revision
  (`/run/current-system`), authenticated by the per-device credential
  (ADR 0008)
- on start and daily: attaches the nixos-facter hardware document
- `410 Gone` means the device is retired: the agent exits permanently
  (exit 3; the systemd unit does not restart on it)
- `401` keeps beating and logs loudly - a re-issued credential picks up
  on restart

## Configuration (environment)

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `SEXTANT_URL` | yes | - | console base URL (https) |
| `SEXTANT_TAG` | yes | - | device asset tag |
| `SEXTANT_CREDENTIAL_FILE` | yes* | - | file with the device credential |
| `SEXTANT_INTERVAL` | no | 60 | seconds between beats |
| `SEXTANT_FACTER` | no | (off) | path to nixos-facter |
| `SEXTANT_FACTS_INTERVAL` | no | 86400 | seconds between facts uploads |

\* under systemd `LoadCredential=credential:...` the file is found
automatically via `CREDENTIALS_DIRECTORY`.

`--once` runs a single beat (timer mode); the default is a jittered loop.

## Deploy

NixOS: `sextant.nixosModules.agent` (see `deploy/nixos/agent.nix`).
The credential is runtime state written once at provisioning; it never
enters the nix store.

## Development

```sh
nix build .#sextant-agent
cd agent && cargo test && cargo clippy --all-targets && cargo fmt --check
```
