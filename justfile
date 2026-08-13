# Sextant task runner. `just ci` is the same gate CI runs.

default: ci

# Full quality gate: format check, vet, lint, race tests with coverage, build.
# ci mirrors .forgejo/workflows/ci.yml - the REAL merge bar. If a step is
# added there, add it here (and vice versa); a narrower local bar teaches
# people the wrong definition of green.
ci: fmt-check vet lint test coverage-floor build nix-build catalog-check css-check agent-ci

# The logic layer must stay above 75%. Transport, ports, logging and the
# capability wiring are excluded: they are glue, and counting them lets real
# coverage rot behind a comfortable average.
#
# 80 is the agreed target (decided 2026-08-06) and this is a ratchet toward
# it, not the destination. It was 70 while the measured number was 75.3,
# which let five points erode before anything would have complained - a floor
# below the standing number protects nothing. Raise this whenever the real
# number clears the next step; never lower it to make a branch pass.
#
# Written as a plain script rather than a one-liner. The previous version used
# Make-style $$ escaping, which just does not do - the shell saw $$ and
# expanded it to a PID, so the recipe printed "coverage: <pid>{total}%" and
# always failed. Nothing noticed, because CI runs its own steps and never
# called this.
coverage-floor:
    #!/usr/bin/env bash
    set -euo pipefail
    # -a: never decide the profile is binary and silently emit nothing. The
    # failure that motivated this said "coverage too low" when the truth was
    # "the file was unreadable", and a check that lies about why is worse than
    # one that does not run.
    grep -a -vE 'internal/ports/|/cmd/|platform/logging|platform/capability' "$COV" > "$COV.logic"
    if [ ! -s "$COV.logic" ]; then
        echo "coverage profile $COV produced no logic-layer lines - refusing to report a number" >&2
        exit 1
    fi
    total=$(go tool cover -func="$COV.logic" | tail -1 | awk '{print $3}' | tr -d '%')
    echo "logic-layer coverage: ${total}%"
    # Raised 70 -> 80 on 2026-08-07; keep this in step with
    # .forgejo/workflows/ci.yml, or local and CI disagree about what passes.
    awk -v t="$total" 'BEGIN { exit (t < 80) ? 1 : 0 }' || {
      echo "coverage below the 80% floor" >&2
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
#
# PER RUN, not a fixed name. It used to be /tmp/sextant-coverage.out for every
# invocation, so two concurrent runs - a background `just ci` and the pre-push
# hook starting its own, which is an ordinary thing to do - interleaved their
# writes into one file. grep then saw the result as binary, printed "binary
# file matches" and emitted NOTHING, the logic profile came out empty, and the
# floor reported "coverage below the 70% floor". Measured on 2026-08-06: a
# clean tree failing its own coverage check for a reason that had nothing to
# do with coverage.
export COV := "/tmp/sextant-coverage." + uuid() + ".out"

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

# The stylesheet is generated but COMMITTED, because it is embedded in the
# binary. So a template that uses a new class, or a rule added to input.css,
# ships as dead markup until somebody remembers `just css` - the page renders
# with the styles simply missing, which looks like a design mistake rather
# than a build one. This is how an edit window shipped invisible on
# 2026-08-13. The guard regenerates and refuses a difference.
css-check:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(mktemp)
    trap 'rm -f "$out"' EXIT
    tailwindcss -c internal/http/web/styles/tailwind.config.js \
      -i internal/http/web/styles/input.css -o "$out" --minify 2>/dev/null
    if ! diff -q "$out" internal/http/web/static/app.css >/dev/null; then
        echo "internal/http/web/static/app.css is out of date - run 'just css'" >&2
        exit 1
    fi

# Regenerate sxctl(1). The page is generated from the CLI's own command list
# and committed, like app.css and catalog.json; a Go test refuses a stale copy,
# so this is the command that test is telling you to run.
man:
    go run ./cmd/sxctl man > docs/man/sxctl.1

build:
    go build -trimpath -o sextant ./cmd/sextant

run: build
    ./sextant --addr 127.0.0.1:8080

# A console you can click through in under a minute, on an example fleet.
#
# The config plane is a git working tree, so a demo needs one: this copies
# examples/overlay somewhere writable and commits it. Everything unsafe is
# explicit rather than defaulted - --dev-auth mints a synthetic owner session
# (loopback only), --gate none skips the Nix validation, and
# --allow-unvalidated makes you say out loud that you meant it.
demo DIR="/tmp/sextant-demo": build
    rm -rf {{DIR}}
    cp -r examples/overlay {{DIR}}
    git -C {{DIR}} init -q -b main
    git -C {{DIR}} add -A
    git -C {{DIR}} -c user.name=demo -c user.email=demo@localhost commit -qm "example fleet"
    @echo
    @echo "  console: http://127.0.0.1:8080   fleet: {{DIR}}"
    @echo
    ./sextant --addr 127.0.0.1:8080 --repo {{DIR}} \
        --dev-auth --gate none --allow-unvalidated --write

clean:
    rm -f sextant sxctl coverage.out coverage.html

# What the FORGE thinks of HEAD. `just ci` passing locally is not the same
# thing, and assuming it was left CI red for twenty commits.
ci-status:
    scripts/ci-status.sh --watch
