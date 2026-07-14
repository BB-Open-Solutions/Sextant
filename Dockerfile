# Base images are pinned by digest (immutable), not by floating tag, so a
# rebuild months from now uses the exact same bytes it did today. To refresh
# to a newer patch release, re-resolve and update both the tag (for humans)
# and the digest (for the pull) with:
#   skopeo inspect docker://golang:1.25-alpine | grep -i digest
#   skopeo inspect docker://alpine:3.22       | grep -i digest
# (or: podman pull <ref> && podman inspect --format '{{.Digest}}' <ref>)

# Build stage: offline, reproducible (vendored deps, trimmed paths).
FROM golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/sextant ./cmd/sextant \
 && CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/sxctl ./cmd/sxctl

# Runtime stage: non-root, tini as PID 1, git for the config plane.
FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
RUN apk add --no-cache tini git ca-certificates \
 && adduser -D -u 65532 sextant
COPY --from=build /out/sextant /usr/local/bin/sextant
COPY --from=build /out/sxctl /usr/local/bin/sxctl
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "sextant"]
CMD ["--addr", "0.0.0.0:8080"]
