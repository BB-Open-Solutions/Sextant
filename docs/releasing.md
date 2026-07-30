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


## Commit and style rules

English, ASCII-only, Conventional Commits, no marketing language. Every commit
carries the trailer naming the assisting model and the human who reviewed and
integrated it. Mirror to the bbopen remote after each commit.

Run `nix develop -c golangci-lint run ./...` before pushing. `go test` does not
run errorlint, and CI has failed on that twice.
