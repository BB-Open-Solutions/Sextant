# Releasing and deploying Sextant

How a release is cut and how it reaches a cluster. Written down because the
steps have an order that matters and two of them have bitten us: the chart
version has to match the tag, and the platform repo has a mirror remote that
must not receive the push.

## The steps

1. bump deploy/helm/Chart.yaml + values.yaml, commit, push bbopen
2. `podman build -q --build-arg VERSION=<v>
   -t forgejo.bb-open.com/bb-open/sextant:<v> . && podman push ...`
   (VERSION voedt sextant_build_info; watch disk: `podman image
   prune -f` if full). No `--target` needed: the server stage is last,
   so the default build is the lean production image.
2b. ONLY when a demo instance runs the simulator - build and push the
   tools image too, or its fleetsim sidecar cannot pull:
   `podman build -q --target tools
   -t forgejo.bb-open.com/bb-open/sextant:<v>-tools . && podman push ...`
   (fleetsim and sxctl live there; the control-plane image deliberately
   does not carry a fake-device generator)
3. platform repo apps/sextant/helmrelease.yaml tag -> `git push origin
   main` (NOT the github-mirror remote - that was a real trap)
4. `flux reconcile source git flux-system -n flux-system` then
   `kustomization sextant` then `source git sextant` then
   `helmrelease sextant -n sextant`
5. verify: rollout status, image tag, /readyz, smoke the new surface


## Releasing DAWO-NixOS (upstream, not ours)

DAWO-NixOS belongs to MinBZK and Rutger maintains it. We contribute through a
fork and a merge request; we never push to it directly. His release rule, and
it is the one we follow:

**The tag is cut from `main` AFTER the merge request is merged. Never from a
branch.**

The `release: mark version <v>` commit travels inside the MR like any other
change. Only the tag waits. Tagging a branch publishes a release nobody
reviewed, and the tag then points at code that `main` does not contain.

That is not hypothetical - it is what we did with `v0.1.1`. The tag sits on
code.overheid.nl pointing at `dc1d667`, which is not reachable from `main`;
`main` is 15 commits behind it. Nothing consumed it (the bb-open overlay pins a
revision on `refs/heads/main`, `4c15a62`, which is `v0.1.0`), so it is
correctable rather than load-bearing.

The order, then:

1. branch on the fork, work, push to the fork
2. open the merge request against MinBZK `main`
3. Rutger reviews and merges
4. `git tag -a v<x.y.z> <merge commit on main>` and push the tag
5. only now does anything downstream pin the new version

## Commit and style rules

English, ASCII-only, Conventional Commits, no marketing language. Every commit
carries the trailer naming the assisting model and the human who reviewed and
integrated it. Mirror to the bbopen remote after each commit.

Run `nix develop -c golangci-lint run ./...` before pushing. `go test` does not
run errorlint, and CI has failed on that twice.
