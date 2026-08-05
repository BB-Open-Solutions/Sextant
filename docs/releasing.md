# Releasing and deploying Sextant

How a release is cut and how it reaches a cluster. Written down because the
steps have an order that matters and two of them have bitten us: the chart
version has to match the tag, and the platform repo has a mirror remote that
must not receive the push.

## The steps

A version TAG is the single lever. `.forgejo/workflows/release.yml` triggers on
`v*` and builds and pushes every image, so no release reaches the registry from
a developer laptop.

1. bump `deploy/helm/Chart.yaml` (version AND appVersion), commit, push both
   remotes
2. `git tag -a v<x.y.z> -m "release <x.y.z>"` and push the tag to bbopen -
   that is what starts the build. Watch it with `scripts/ci-status.sh`.
3. platform repo `apps/sextant/helmrelease.yaml` tag -> `git push origin main`.
   Bump BOTH tags in that file: the console image and `gateRunner.image`,
   which is a separate image and stays behind if you only change the first.
   `apps/sextant-docs` pins its own image in `deployment.yaml` and needs the
   same bump, or the published handbook keeps serving the build it was pinned
   at. It is also a separate Flux Kustomization, so it rolls on its own
   schedule - check the pod, not the commit.

   **On the mirror.** This step used to say "NOT the github-mirror remote -
   that was a real trap". Measured on 2026-08-05: `origin` in the platform
   repo carries TWO pushurls (`git config --get-regexp remote.origin.pushurl`),
   forgejo and `github.com/brambuijs/bb-open-platform-v2`. So the documented
   command reaches GitHub whether or not you name the mirror remote, and has
   been doing so for previous releases too. Following the warning does not
   avoid what it warns about. Either the config is intended and this note
   should say so plainly, or the pushurl should go - but the instruction as it
   stood described something that was not happening.
4. `flux reconcile source git flux-system -n flux-system` then
   `kustomization sextant` then `source git sextant` then
   `helmrelease sextant -n sextant`
5. verify: rollout status, image tag, /readyz, smoke the new surface

**What this replaced, and why it is written down.** These steps used to say
"podman build ... && podman push" from your own machine. The workflow arrived
and the runbook did not change, so everyone kept following the runbook. Console
and gate images were built by hand and reached 0.78.0; `sextant-docs` is the one
image nobody builds by hand, so it silently stopped at 0.65.9 on 16 July and
the published handbook froze there. There were no version tags at all between
v0.65.9 and v0.79.0, so no released image could be traced to a commit.

If you ever need to build by hand again, `--build-arg VERSION=<v>` feeds
`sextant_build_info`, and the tools image (`--target tools`, carrying fleetsim
and sxctl) is only needed when a demo instance runs the simulator sidecar.

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
