# Base images are pinned by digest (immutable), not by floating tag, so a
# rebuild months from now uses the exact same bytes it did today. To refresh
# to a newer patch release, re-resolve and update both the tag (for humans)
# and the digest (for the pull) with:
#   skopeo inspect docker://golang:1.25-alpine | grep -i digest
#   skopeo inspect docker://alpine:3.22       | grep -i digest
# (or: podman pull <ref> && podman inspect --format '{{.Digest}}' <ref>)

# Build stage: offline, reproducible (vendored deps, trimmed paths).
FROM docker.io/library/golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY internal/ internal/
# VERSION lands in sextant_build_info; pass the release tag:
#   podman build --build-arg VERSION=<v> ...
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/sextant ./cmd/sextant \
 && CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/sxctl ./cmd/sxctl \
 && CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/fleetsim ./cmd/fleetsim

# Runtime base: non-root, tini as PID 1, git for the config plane. Shared by
# both runtime targets below so they cannot drift apart.
FROM docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce AS runtime-base
RUN apk add --no-cache tini git ca-certificates \
 && adduser -D -u 65532 sextant
USER 65532:65532
EXPOSE 8080

# The TOOLS image: the simulator and the CLI, for the demo instance's sidecar
# and for anyone who wants sxctl in a container. Built explicitly:
#   podman build --target tools -t <ref>:<v>-tools .
# Keeping it a separate tag means the demo can run a simulated fleet without
# the production image carrying the means to do so.
FROM runtime-base AS tools
COPY --from=build /out/sxctl /usr/local/bin/sxctl
COPY --from=build /out/fleetsim /usr/local/bin/fleetsim
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["fleetsim", "--help"]

# The PRODUCTION image: the server and nothing else. Deliberately the LAST
# stage, because a build without --target builds the last one - so a plain
# `podman build .` yields the lean image and shipping the extras has to be
# asked for by name.
#
# fleetsim used to ride along in here. It drives a simulated fleet against a
# console and needs nothing but the shared check-in token, so shipping it in
# the control plane put a ready-made fake-device generator inside the very
# container an attacker would want it in. sxctl went the same way: the console
# is administered through its API and its UI, not by shelling into the pod, so
# a CLI in the image is reach it does not need. Both are still built, in the
# tools target above, which is what the demo deployment pulls.
FROM runtime-base AS server
COPY --from=build /out/sextant /usr/local/bin/sextant
ENTRYPOINT ["/sbin/tini", "--", "sextant"]
CMD ["--addr", "0.0.0.0:8080"]
