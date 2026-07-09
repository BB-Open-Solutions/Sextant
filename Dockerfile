# Build stage: offline, reproducible (vendored deps, trimmed paths).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/ web/
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/sextant ./cmd/sextant \
 && CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/dfctl ./cmd/dfctl

# Runtime stage: non-root, tini as PID 1, git for the config plane.
FROM alpine:3.22
RUN apk add --no-cache tini git ca-certificates \
 && adduser -D -u 65532 sextant
COPY --from=build /out/sextant /usr/local/bin/sextant
COPY --from=build /out/dfctl /usr/local/bin/dfctl
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "sextant"]
CMD ["--addr", "0.0.0.0:8080"]
