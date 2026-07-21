# Inspoelstraat: appliance -> overlay (fase 2)

Doel: dawo-inspoelstraat bouwt uit `sextant-overlay-bbopen` en volgt
`rings/infra`, zoals elk vloot-device. Zolang dat niet zo is kan geen
enkele fleet-wide rollout afronden (het station komt nooit "on target");
daarom staat infra nu tijdelijk niet in de ladder.

## Stand

- Overlay heeft de station-class compleet (`flake.nix` station-stack:
  provisioning, ci-runner, station-agent, station-runner, SB+TPM2).
- Device-record bestaat (`fleet.json`: class station, hardware msi-cubi,
  groep infra) en het station praat al met de console (check-ins, jobs)
  via de appliance-config uit `inspoelstraat-appliance`.
- Gat: de draaiende machine bouwt nog de appliance-flake, niet de
  overlay, en autoUpdate volgt niet `rings/infra`.

## Stappen (uitvoeren met SSH-toegang op het station)

1. Vooraf, eenmalig: `rings/infra` gelijkzetten aan `main` (anders
   switcht het station straks naar een maanden-oude ring-rev):
   `git -C bb-open push bbopen main:rings/infra --force-with-lease`
2. Op het station de overlay-config activeren:
   `nixos-rebuild switch --flake \
     git+https://forgejo.bb-open.com/bb-open/sextant-overlay-bbopen?ref=rings/infra#dawo-inspoelstraat`
   (eerste keer handmatig; daarna neemt comin/autoUpdate het over met
   pollSeconds=120 uit de station-stack).
3. Credential check: `/var/lib/sextant-agent/credential` moet blijven
   staan (agent-identiteit; de overlay-module leest hetzelfde pad).
4. Verifieer:
   - `systemctl status comin sextant-agent sextant-station-runner`
   - console Devices: dawo-inspoelstraat rapporteert de overlay-revisie
   - een test-image-job claimen lukt nog (station-runner).
5. Ladder herstellen: infra-ring terug in het waveplan (org updates ->
   advanced, of fleet.json), soak/approval naar smaak.
6. Appliance-repo: markeer de station-host als gemigreerd; de
   -install/-sb bring-up varianten blijven daar voor herimaging.

## Risico's

- SB+TPM2: de overlay-stack zet secureboot + TPM2-unlock aan; de MSI is
  al enrolled (steady state), dus een switch hoort geen firmware-actie
  te vragen. Bij twijfel: eerst `nixos-rebuild build` en de diff van
  systemd-boot entries bekijken.
- Rollback: oude generation blijft in het boot-menu; boot-health rolt
  een kapotte switch zelf terug.
