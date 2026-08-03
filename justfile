# Sextant task runner. `just ci` is the same gate CI runs.

default: ci

# Full quality gate: format check, vet, lint, race tests with coverage, build.
# ci mirrors .forgejo/workflows/ci.yml - the REAL merge bar. If a step is
# added there, add it here (and vice versa); a narrower local bar teaches
# people the wrong definition of green.
ci: fmt-check vet lint test coverage-floor build nix-build catalog-check agent-ci

# The logic layer must stay above 70%. Transport, ports, logging and the
# capability wiring are excluded: they are glue, and counting them lets real
# coverage rot behind a comfortable average.
#
# Written as a plain script rather than a one-liner. The previous version used
# Make-style $$ escaping, which just does not do - the shell saw $$ and
# expanded it to a PID, so the recipe printed "coverage: <pid>{total}%" and
# always failed. Nothing noticed, because CI runs its own steps and never
# called this.
coverage-floor:
    #!/usr/bin/env bash
    set -euo pipefail
    grep -vE 'internal/ports/|/cmd/|platform/logging|platform/capability' "$COV" > "$COV.logic"
    total=$(go tool cover -func="$COV.logic" | tail -1 | awk '{print $3}' | tr -d '%')
    echo "logic-layer coverage: ${total}%"
    awk -v t="$total" 'BEGIN { exit (t < 70) ? 1 : 0 }' || {
      echo "coverage below the 70% floor" >&2
      exit 1
    }

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

# COV is outside the repository, and that is the whole point. Writing
# coverage.out into the tree changes the directory's NAR hash while
# gate_e2e_test.go has that same path pinned as a flake input, so the eval
# fails partway through the run with a hash mismatch that has nothing to do
# with the code. Gitignoring it does not help: a path: flake input hashes what
# is on disk, not what git tracks.
export COV := "/tmp/sextant-coverage.out"

test:
    go test -race -coverprofile="$COV" ./...

cover: test
    go tool cover -func="$COV" | tail -1

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

# What the FORGE thinks of HEAD. `just ci` passing locally is not the same
# thing, and assuming it was left CI red for twenty commits.
ci-status:
    scripts/ci-status.sh --watch
