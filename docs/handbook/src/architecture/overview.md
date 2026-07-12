# Architecture overview

Sextant is a hexagonal Go application: a pure domain (model, resolver, policy
compiler, filter evaluator) under application services, behind ports, with
adapters for git, Nix, Postgres, LDAP and OIDC, and two thin transports - a
server-rendered console and a JSON API over the same services.

- **Config plane** - the git overlay (`fleet.json` + catalog) is the source of
  truth. Writes are serialized, pass the Nix eval gate, and commit with
  SSO-attributed authorship.
- **Observed plane** - device check-ins, posture and hardware facts live in
  Postgres, tenant-namespaced.
- **Imaging plane** - discovery and image jobs drive provisioning from a station.

See the decision records for the reasoning behind each choice.
