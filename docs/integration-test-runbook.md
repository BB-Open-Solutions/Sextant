# Integration test round (#72): NetBird, Wazuh, LDAP, Zitadel

Goal: enrol a real device (t495s) into the bundled integrations through
Sextant settings - proof that "a municipality buys the bundle" works. The
order is deliberate: the mesh first, because LDAP (and later Wazuh) are
cluster-internal and become reachable through the mesh.

## State (18 July)

| Integration | Infra | Overlay config | Blocker |
|---|---|---|---|
| NetBird | management: https://netbird.bb-open.com (live) | empty | reusable setup key (Bram, dashboard) |
| LDAP | ldap-bb-open in the cluster; sextant-ro bind proven (#29) | empty | route device->LDAP (mesh or LDAPS expose: a design choice) |
| Wazuh | NO manager found | empty | stand up a manager (separate platform app) |
| Zitadel | live; console RBAC works | n/a | role verification only |

## Phase 1 - NetBird on the t495s (the key to the rest)
1. Bram: create a reusable setup key (dashboard netbird.bb-open.com), put
   the value in the overlay as the agenix secret `netbird-setup-key` plus a
   secret reference in fleet.json.
2. Console: on group zaanstad (or pilot) set `netbird.enable=true`,
   `netbird.managementUrl=https://netbird.bb-open.com`,
   `netbird.setupKey=<secret-ref>`.
3. Merge -> delivery-on-merge rolls it out (test wave -> group). Proof: the
   t495s appears as a peer in the NetBird dashboard.

## Phase 2 - LDAP login on the device
The design choice comes first (Bram): (a) LDAP over the mesh (the cluster
then needs a netbird router/exit) or (b) LDAPS public with a strict bind
policy. Then: identity.enable + provider ldap + ldapUri + searchBase +
bindDN + bindSecret (agenix) on the group; proof = an LDAP user can log in
on the t495s.

**Recommendation (21 July): (a) over the mesh - and that choice covers
phase 3 at the same time.** LDAP (1636) and Wazuh (1514/1515) are both
cluster-internal TCP services that devices must reach; exposing them
publicly means maintaining two TCP ingresses plus a bind policy, whereas the
mesh makes both an internal address. Required: one netbird routing peer in
the cluster (a subnet route to the service CIDR, or two service IPs), once.
Same sovereignty line as the rest: nothing extra on the public internet.

## Phase 3 - Wazuh
The manager does not exist yet: first `apps/wazuh` in bb-open-platform-v2,
then wazuh.enable + manager address (mesh address, see phase 2) +
enrollmentSecret per group; proof = the agent appears in the Wazuh console
and the agentGroup per group is right. Minimal first step: the wazuh
manager only (enrollment 1515 + agent traffic 1514), without
indexer/dashboard - enough for the enrolment proof; the full stack is a
later platform job.

## Phase 4 - Zitadel roles
Console side: with a dawo-support account (Editor) and a dawo-beheer account
(Owner), replay the RBAC boundaries (merging must fail as Editor and succeed
as Owner; four-eyes forces a second person).

## What each phase tests in Sextant itself
Every enable runs through the whole new machinery: CR -> gate -> merge ->
auto-scoped run -> test wave sign-off -> wave -> the agent applies
secrets/services. The integration test round is therefore also the second
full e2e of the update chain on real hardware.
