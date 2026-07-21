# 0007 - Secret backends beyond agenix (key vault)

Status: draft for the vault experiment (task list #14). No code yet.

## Where we stand

A secret is split in two by design: the console registers only a NAME
(`fleet.json` secretRefs, audited commit), the MATERIAL lives outside
the console and resolves on the device to `/run/agenix/<name>`
(integrations.nix `secretPath`). Today the material path is agenix:
age-encrypted files in the overlay repo, decrypted by the device's SSH
host key at activation.

That split is already backend-agnostic: nothing in the console, the
catalog or the generator knows HOW the file under /run gets there.

## What a vault adds

- Rotation without a git commit per device-set (agenix re-encrypts per
  recipient change; a vault rotates centrally).
- Audit of reads, not just writes.
- Enterprise expectation: municipalities ask "does it integrate with a
  vault" in tenders.

## Candidate: OpenBao

Open-source Vault fork under the Linux Foundation - the sovereign
choice, self-hostable in the platform. Azure Key Vault mirrors the
Intune idiom but contradicts the sovereignty line; Kubernetes Secrets
only cover the cluster side, not devices.

## Experiment shape

1. OpenBao in the platform (single node, KV v2), reachable over the
   mesh (same routing-peer decision as LDAP/Wazuh - integration
   runbook fase 2).
2. Device side: a small oneshot/timer unit ("vault-agent-lite") that
   authenticates with the device identity and writes each mapped
   secret to `/run/secrets/<name>` (tmpfs), then a compatibility
   symlink `/run/agenix/<name>` so integrations.nix stays untouched.
   Auth method to prove: TLS cert or AppRole bound to the device
   credential; the agent's per-device credential is the natural seed.
3. Overlay option `dawo.secrets.backend = "agenix" | "openbao"` plus
   the vault address; per-device mapping stays the secretRefs list -
   no console change at all for the experiment.

## Success criteria

- The NetBird setup key delivered via OpenBao instead of the .age file,
  same console configuration, device enrolls identically.
- Rotate the key in OpenBao; device picks it up without a git commit.
- Console still stores nothing but the name (verify by inspection).

## Out of scope (for the experiment)

Dynamic secrets, PKI issuance, per-tenant vault namespaces (cells,
ADR 0009) - note them in the ADR that follows a successful experiment.
