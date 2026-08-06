# 0022 - The console owns the credential it pushes with, and an admin can replace it

## Status

Accepted 2026-08-07 (Bram): "At deploy time we need a way to supply the git
user Sextant runs as, with minimal rights. On top of that, an admin has to
be able to rotate the password and the key from the interface."

Closes audit finding H2 (`docs/audit/security-2026-08.md`). Does not change
ADR 0012's fail-closed gate or ADR 0005's config-as-data rule.

## Context

Sextant writes configuration by committing and pushing to a git forge. It
authenticates with a netrc, written at pod start by an init container from a
mounted Kubernetes secret (`gitRemote.netrcSecret`).

Three things follow from that, and all three were measured at bb-open on
2026-08-06:

1. **The account was a person's.** The netrc read `login bram.buijs`. Every
   machine push - a ring promotion, a settings change, an approved change
   request - landed in the forge's history under a human's name. The audit
   trail says a person did what a program did, and the blast radius of that
   credential is everything that person may do on the forge, not what the
   console needs.

2. **Rotating it required cluster access.** `kubectl edit secret`, then a
   restart. So the set of people who can rotate the credential is the set of
   people with cluster admin, which is not the set of people responsible for
   the forge account. A credential that is awkward to rotate does not get
   rotated: this one had not been, ever.

3. **Nothing recorded a rotation.** A secret edit leaves no trace in
   Sextant's audit log.

The requirement has two halves and they pull in different directions. "Give
the console a minimal-rights account at deploy time" wants the credential to
come from the deployment. "An admin can rotate it from the interface" wants
the console to own it. Both are legitimate: the first is how a cell is
provisioned, the second is how it is operated for years afterwards.

## Options

**A. Console writes the Kubernetes secret.** Give the pod RBAC on its own
secret and rotate there. Keeps one source of truth. Costs a Kubernetes
dependency in an application that otherwise has none, plus a service account
that can write secrets - a new and fairly sharp privilege - and it does not
work outside Kubernetes at all.

**B. Console stores the credential itself and writes the netrc.** The netrc
lives on the console's own volume, the same one holding the overlay clone.
Nothing new is required to write it.

**C. Keep it mounted; document a rotation runbook.** Free. Changes nothing
about who can rotate, which is the actual finding.

## Decision

**Option B, with the mount kept as the fallback.**

- The credential is stored per tenant, sealed with the app key, in
  `git_identity`. The token is never returned to a caller, never rendered,
  and never logged. The host, the account and who last replaced it are, since
  those are what an operator needs to answer "whose account is this".
- On startup and on every rotation, the console writes `$HOME/.netrc`
  itself. git reads that file per invocation, so a rotation takes effect on
  the next push with **no restart**.
- **With nothing stored, behaviour is exactly as before**: the mounted netrc
  governs. An upgrade changes nothing until an admin chooses to store one,
  and clearing the stored credential returns to the mount.
- The page is owner-only, and a rotation is an ordinary audited action.

Deploy-time provisioning therefore keeps working the way it does today -
that half of the requirement was already met by `gitRemote.netrcSecret` - and
the operating half is what this ADR adds.

**Minimal rights is documented, not enforced.** Sextant cannot verify what a
forge account may do; only the forge can. What the console needs is write
access to the overlay repository and nothing else - no forge admin, no other
repositories - and the page says so where the account is entered. Anything
stronger would be a claim we cannot check, and a claim we cannot check is
worse than a sentence that tells the truth.

## Consequences

- Second place Sextant holds a secret at rest, after the SMTP password
  (`0009_smtp.sql`). Same justification: the alternative is an operator
  keeping it somewhere worse. Both are sealed with `SEXTANT_SECRET_KEY`, so
  both are lost together if that key is lost - which is an argument for the
  key-management work, not against this.
- A deployment without a sealing key or without a writable home cannot store
  a credential. It gets a page that names the missing precondition rather
  than a form that silently fails.
- If the sealing key is rotated and the stored blob cannot be decrypted, the
  console logs it and carries on with the mounted credential. Startup never
  fails over this: a console that will not start cannot be used to fix it.
- The netrc is written via a temporary file and a rename. A push may be in
  flight, and truncate-then-write would hand a concurrent git a half-written
  line - which surfaces as an authentication failure and reads like a wrong
  password.
- **bb-open still pushes as a person.** This ADR ships the mechanism; the
  machine account has to be created on the forge and entered, and until that
  happens H2 is open. The mechanism being available is not the fix.

## Verification

Closed when a machine account exists on the forge with write access to the
overlay repository and nothing else, it is entered in the console, and a real
change request merges and pushes under that account. Until a push has landed
under the machine name, this is a feature and not a fix.
