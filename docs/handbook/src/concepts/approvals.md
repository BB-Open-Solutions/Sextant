# Approval flows

How changes and updates are reviewed before they take effect is configurable
per organisation, under **Access -> Approval flows** (owner):

- **Four-eyes** - a change may not be merged by its own author.
- **Require change-request** - configuration edits must go through a reviewed
  change, never a direct commit.
- **Require test wave** - a rollout must have a gated test wave first; an owner
  can skip it per rollout.

Every configuration change is a git commit that passes the Nix gate first, so
an edit that would not build never reaches the fleet.
