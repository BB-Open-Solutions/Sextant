# Imaging station: appliance -> overlay (phase 2)

Goal: dawo-inspoelstraat builds from `sextant-overlay-bbopen` and follows
`rings/infra`, like every other fleet device. Until it does, no fleet-wide
rollout can finish (the station never reaches "on target"); that is why
infra was left out of the ladder.

## State: done, 2026-08-10

Re-read against the running machine rather than against this document, and
the document was wrong. It said the station still built the appliance flake.
Measured:

- `comin.yaml` on the station carries `branches.main.name: rings/infra`,
  `operation: switch`. It has been following the overlay, not the appliance.
- Moving `rings/infra` was enough. comin picked it up on its own poll, built,
  and switched at 10:00 local; the agent restart in the journal is that
  switch. **The manual `nixos-rebuild switch` below was not needed.**
- The console has the station at revision `a0f5236`, checking in seconds ago.
- `/var/lib/sextant-agent/credential` survived: 63 bytes, root-only, dated
  2026-08-06.

So the migration had largely already happened and nobody wrote it down. What
follows is kept because a station will be rebuilt one day, not because it is
outstanding work.

**Two units read as `inactive` and that is correct.** `dawo-station-runner`
is `Type = oneshot` driven by a systemd timer, and `sextant-actd` is oneshot
too; both are idle between runs. An earlier version of this runbook told you
to check `sextant-station-runner`, which **does not exist** - the name is
`dawo-station-runner`. `systemctl status` on a name that does not exist says
"could not be found", which reads exactly like a broken migration. Check the
timer instead:

```
systemctl list-timers dawo-station-runner --no-pager
```

## Steps, if a station has to be brought over again

1. Once, beforehand: set `rings/infra` to the revision you want. Note what
   that revision now carries - on 2026-08-10 the ring was **ten commits**
   behind main, including a core pin bump. Everything arrives at once, and a
   failure is then unattributable:
   `git -C bb-open push bbopen <rev>:rings/infra --force-with-lease`

   Split anything that changes device login transport into its own step. A
   bad `ldaps://` setting is the one change that can stop people logging in,
   and it should never share a switch with a core bump.

2. If comin already follows `rings/infra`, there is nothing else to do: wait
   one poll (120s for the station stack) and watch. Only if it does not:
   `nixos-rebuild switch --flake \
     git+https://forgejo.bb-open.com/bb-open/sextant-overlay-bbopen?ref=rings/infra#dawo-inspoelstraat`

   Do not run this while comin is mid-build. Two things then race for the
   system profile and comin can switch back to what it was building.

3. Credential check: `/var/lib/sextant-agent/credential` must stay in place
   (agent identity; the overlay module reads the same path). It is root-only,
   so `ls` without sudo says "permission denied" rather than "missing".

4. Verify:
   - `systemctl is-active comin sextant-agent` - both active
   - `systemctl list-timers dawo-station-runner` - the timer is armed
   - console Devices: the station reports the ring's revision
   - claiming a test image job still works

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
