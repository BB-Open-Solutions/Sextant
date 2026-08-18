# Publishing Sextant on the Proxy platform

The Proxy marketplace (`ProxyBeheerdeDiensten/charts`) keeps every app in its
own repository as `apps/<name>/`, with the chart vendored underneath it. That
is a second copy of a chart we already maintain here, and a second copy is a
drift waiting to happen: the interesting failure is not that they differ, it
is that nobody notices until a customer installs the older one.

So the copy is generated, never edited:

```
./deploy/marketplace/sync.sh ../charts     # a checkout of their repo
```

It writes `apps/sextant/metadata.yaml` and `apps/sextant/helm/` from this
repository's `deploy/helm`, and refuses when the chart version and the
listing version in `metadata.yaml` disagree.

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
