# Imaging station: appliance -> overlay (phase 2)

Goal: dawo-inspoelstraat builds from `sextant-overlay-bbopen` and follows
`rings/infra`, like every other fleet device. Until it does, no fleet-wide
rollout can finish (the station never reaches "on target"); that is why
infra is currently left out of the ladder.

## State

- The overlay has the station class complete (`flake.nix` station stack:
  provisioning, ci-runner, station-agent, station-runner, SB+TPM2).
- The device record exists (`fleet.json`: class station, hardware msi-cubi,
  group infra) and the station already talks to the console (check-ins,
  jobs) through the appliance config from `inspoelstraat-appliance`.
- Gap: the running machine still builds the appliance flake rather than the
  overlay, and autoUpdate does not follow `rings/infra`.

## Steps (run with SSH access on the station)

1. Once, beforehand: set `rings/infra` equal to `main`, or the station will
   switch to a ring revision that is months old:
   `git -C bb-open push bbopen main:rings/infra --force-with-lease`
2. Activate the overlay config on the station:
   `nixos-rebuild switch --flake \
     git+https://forgejo.bb-open.com/bb-open/sextant-overlay-bbopen?ref=rings/infra#dawo-inspoelstraat`
   (by hand the first time; after that comin/autoUpdate takes over with
   pollSeconds=120 from the station stack).
3. Credential check: `/var/lib/sextant-agent/credential` must stay in place
   (agent identity; the overlay module reads the same path).
4. Verify:
   - `systemctl status comin sextant-agent sextant-station-runner`
   - console Devices: dawo-inspoelstraat reports the overlay revision
   - claiming a test image job still works (station-runner).
5. Restore the ladder: infra ring back into the wave plan (org updates ->
   advanced, or fleet.json), soak/approval to taste.
6. Appliance repo: mark the station host as migrated; the -install/-sb
   bring-up variants stay there for re-imaging.

## Risks

- SB+TPM2: the overlay stack turns on secure boot and TPM2 unlock; the MSI
  is already enrolled (steady state), so a switch should not ask for a
  firmware action. When in doubt, run `nixos-rebuild build` first and look
  at the diff of systemd-boot entries.
- Rollback: the old generation stays in the boot menu, and boot-health
  rolls a broken switch back by itself.
