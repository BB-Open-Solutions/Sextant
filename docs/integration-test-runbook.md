# Integratie-testronde (#72): NetBird, Wazuh, LDAP, Zitadel

Doel: een echt device (t495s) via Sextant-settings laten enrollen in de
bundel-integraties — bewijs dat "gemeente koopt bundel" werkt. Volgorde is
bewust: de mesh eerst, want LDAP (en straks Wazuh) zijn cluster-intern en
worden via de mesh bereikbaar.

## Stand (18 jul)

| Integratie | Infra | Overlay-config | Blokkade |
|---|---|---|---|
| NetBird | management: https://netbird.bb-open.com (live) | leeg | reusable setup-key (Bram, dashboard) |
| LDAP | ldap-bb-open in cluster; sextant-ro-bind bewezen (#29) | leeg | route device→LDAP (mesh of LDAPS-expose: ontwerpkeuze) |
| Wazuh | GEEN manager gevonden | leeg | manager opzetten (aparte platform-app) |
| Zitadel | live; console-RBAC werkt | n.v.t. | alleen rollen-verificatie |

## Fase 1 — NetBird op t495s (de sleutel voor de rest)
1. Bram: reusable setup-key aanmaken (dashboard netbird.bb-open.com),
   waarde als agenix-secret `netbird-setup-key` in de overlay + secret-ref
   in fleet.json.
2. Console: op groep zaanstad (of pilot) `netbird.enable=true`,
   `netbird.managementUrl=https://netbird.bb-open.com`,
   `netbird.setupKey=<secret-ref>`.
3. Merge → delivery-on-merge rolt het uit (testwave → groep). Bewijs:
   t495s verschijnt als peer in het NetBird-dashboard.

## Fase 2 — LDAP-login op het device
Ontwerpkeuze eerst (Bram): (a) LDAP over de mesh (cluster heeft dan een
netbird-router/exit nodig) of (b) LDAPS publiek met strikte bind-policy.
Daarna: identity.enable + provider ldap + ldapUri + searchBase + bindDN +
bindSecret (agenix) op de groep; bewijs = LDAP-user kan inloggen op t495s.

## Fase 3 — Wazuh
Manager bestaat nog niet: eerst `apps/wazuh` in bb-open-platform-v2 (of
VPS), dan wazuh.enable + manager-adres + enrollmentSecret per groep;
bewijs = agent meldt zich in de Wazuh-console, agentGroup per groep klopt.

## Fase 4 — Zitadel-rollen
Console-kant: met een dawo-support-account (Editor) en een dawo-beheer-
account (Owner) de RBAC-grenzen naspelen (merge mag niet als Editor, wel
als Owner; vier-ogen dwingt tweede persoon af).

## Wat elke fase test in Sextant zelf
Elke enable loopt door de volledige nieuwe machinerie: CR → gate →
merge → auto-scoped-run → testwave-aftekenen → wave → agent past secrets/
services toe. De integratie-testronde is dus meteen de tweede volledige
e2e van de updates-keten op echt ijzer.
