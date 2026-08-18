# Publishing Sextant on the Proxy platform

The Proxy marketplace (`ProxyBeheerdeDiensten/charts`) keeps every app in its
own repository as `apps/<name>/`, with the chart vendored underneath it. That
is a second copy of a chart we already maintain here, and a second copy is a
drift waiting to happen: the interesting failure is not that they differ, it
is that nobody notices until a customer installs the older one.

So the copy is generated, never edited, and it lives in a repository of our
own rather than in theirs:

**<https://forgejo.bb-open.com/bb-open/sextant-charts>**

Their tag convention documents this shape: a listing points at a `source_url`
with a `chart_path`, and their own monorepo is only one way to satisfy it. A
separate repository means no pull request to them per release, no fork, and
one place where the chart is authored.

The release workflow writes it. `sync.sh` does the work and can be run by hand
against any checkout with an `apps/` directory:

```
./deploy/marketplace/sync.sh /path/to/sextant-charts
```

It writes `apps/sextant/metadata.yaml` and `apps/sextant/helm/` from this
repository's `deploy/helm`, and refuses when the chart version and the listing
version in `metadata.yaml` disagree.

## What the release needs from an operator, once

A repository secret `MIRROR_TOKEN` on DAWO-Sextant's forgejo mirror, holding a
token that may push to `bb-open/sextant-charts`. Without it a release still
publishes images and says, in the job log, that the marketplace mirror is now
a version behind and how to catch it up. It does not fail the release: the
images are the release, the mirror is the shop window.

## Their release convention

The listing version in the marketplace database is bare semver (`0.88.0`);
the git tag in their repository is `sextant-0.88.0`. Their backend resolves
the tag by suffix. Bare version tags are explicitly not allowed there, because
in a monorepo they are ambiguous.

## What is not decided yet

The collection: NetBird, Wazuh and OpenBao alongside the console, so a
customer gets the stack rather than a console with prerequisites. Their plugin
mechanism reads `helm/charts/<plugin>/Chart.yaml`, which means subcharts are
vendored in-tree, and that is the part worth thinking about before doing:

- **Vendored subcharts.** One listing, one install, one version to support.
  We then carry and upgrade someone else's Wazuh and NetBird charts, and their
  `openbao` app already exists separately, so it would exist twice.
- **Separate apps, thin wiring.** Each server is its own listing, upgradable
  on its own cadence, and Sextant's plugins carry only the integration
  settings it already understands. Smaller, and closer to how that repository
  is laid out today.

The second is the recommendation. It is also the one that does not put us in
the business of shipping other projects' servers.
