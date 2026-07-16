# Sextant task runner. `just ci` is the same gate CI runs.

default: ci

# Full quality gate: format check, vet, lint, race tests with coverage, build.
# ci mirrors .forgejo/workflows/ci.yml - the REAL merge bar. If a step is
# added there, add it here (and vice versa); a narrower local bar teaches
# people the wrong definition of green.
ci: fmt-check vet lint test coverage-floor build nix-build catalog-check agent-ci

coverage-floor:
    @bash -c 'grep -vE "internal/ports/|/cmd/|platform/logging|platform/capability" coverage.out > coverage-logic.out; \
      total=$(go tool cover -func=coverage-logic.out | tail -1 | awk "{print \$$3}" | tr -d "%"); \
      echo "logic-layer coverage: $${total}%"; \
      awk -v t="$$total" "BEGIN { exit (t < 70) ? 1 : 0 }" || (echo "coverage below 70% floor" && exit 1)'

nix-build:
    nix build .#sextant

catalog-check:
    examples/overlay/regen-catalog.sh --check

agent-ci:
    cd agent && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test

fmt:
    gofmt -w cmd internal

fmt-check:
    @test -z "$(gofmt -l cmd internal)" || (gofmt -l cmd internal && echo "gofmt: files need formatting" && exit 1)

vet:
    go vet ./...

lint:
    golangci-lint run ./...

test:
    go test -race -coverprofile=coverage.out ./...

cover: test
    go tool cover -func=coverage.out | tail -1

# Regenerate the console stylesheet from the Tailwind sources. The output
# (internal/http/web/static/app.css) is committed and embedded, like the
# vendored htmx.min.js; run this after changing templates or styles.
css:
    tailwindcss -c internal/http/web/styles/tailwind.config.js \
      -i internal/http/web/styles/input.css \
      -o internal/http/web/static/app.css --minify

build:
    go build -trimpath -o sextant ./cmd/sextant

run: build
    ./sextant --addr 127.0.0.1:8080

clean:
    rm -f sextant sxctl coverage.out coverage.html
