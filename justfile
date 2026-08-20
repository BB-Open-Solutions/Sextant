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

# A console you can click through in under a minute, on a simulated fleet.
#
# Everything unsafe is explicit rather than defaulted: --dev-auth mints a
# synthetic owner session (loopback only), --gate none skips the Nix
# validation, and --allow-unvalidated makes you say out loud that you meant
# it. Nothing leaves this machine and nothing outside DIR is written.
#
# WHY A DATABASE. The observed plane lives in Postgres, so without one the
# console mounts three capabilities instead of five: no device status, and
# /station answers 503. Measured 2026-08-20. That is half a product - the
# imaging line and the fleet view are the two things worth showing - so the
# demo boots a throwaway Postgres of its own, on a unix socket inside DIR,
# with initdb and pg_ctl. No container, no port, no root, and it is deleted
# on the way out.
demo DIR="/tmp/sextant-demo" DEVICES="60": build
    #!/usr/bin/env bash
    set -euo pipefail
    dir="{{DIR}}"
    port=8080

    for bin in initdb pg_ctl createdb; do
        command -v "$bin" >/dev/null || {
            echo "demo needs $bin on PATH (nix develop, or your distro's postgresql package)" >&2
            exit 1
        }
    done


    rm -rf "$dir"
    mkdir -p "$dir/pg/sock"
    # Into the demo directory, not the repo root: the simulator is a build
    # artifact and the tree stays clean, which also means the demo cannot
    # leave a binary behind for git to notice.
    go build -trimpath -o "$dir/fleetsim" ./cmd/fleetsim

    # One trap for everything: a demo you have to clean up by hand is a demo
    # that leaves a Postgres running on somebody's laptop for a week.
    cleanup() {
        set +e
        [ -n "${sim_pid:-}" ] && kill "$sim_pid" 2>/dev/null
        [ -n "${console_pid:-}" ] && kill "$console_pid" 2>/dev/null
        pg_ctl -D "$dir/pg/data" -m immediate stop >/dev/null 2>&1
        rm -rf "$dir"
        echo
        echo "  demo stopped, $dir removed"
    }
    # Ctrl-c is how this demo is meant to end, so it exits 0. Letting bash's
    # 130 through made `just` print "recipe failed", which reads as a broken
    # demo to the one person you least want to confuse: someone trying it for
    # the first time.
    trap cleanup EXIT
    trap 'exit 0' INT TERM

    echo "  postgres..."
    initdb -D "$dir/pg/data" -U sextant --auth=trust -E UTF8 >/dev/null
    pg_ctl -D "$dir/pg/data" -o "-k $dir/pg/sock -h ''" -l "$dir/pg/log" -w start >/dev/null
    createdb -h "$dir/pg/sock" -U sextant sextant

    # The config plane is a git working tree, so the demo needs one: the
    # example overlay carries the catalog, profiles and bundles, and the
    # generator replaces its single example device with a fleet worth
    # looking at, wave plan included.
    echo "  fleet..."
    cp -r examples/overlay "$dir/overlay"
    "$dir/fleetsim" -gen {{DEVICES}} > "$dir/overlay/fleet.json"
    git -C "$dir/overlay" init -q -b main
    git -C "$dir/overlay" add -A
    git -C "$dir/overlay" -c user.name=demo -c user.email=demo@localhost commit -qm "simulated fleet"
    # A ring branch per group: this is what a device follows, and without
    # them the simulated agents have no target to converge on.
    for g in $(python3 -c 'import json,sys; print(" ".join(json.load(open(sys.argv[1]))["groups"]))' "$dir/overlay/fleet.json"); do
        git -C "$dir/overlay" branch -q "rings/$g" main
    done

    export SEXTANT_PG_DSN="postgres://sextant@/sextant?host=$dir/pg/sock"
    export SEXTANT_CHECKIN_TOKEN="demo-checkin-token"
    ./sextant --addr "127.0.0.1:$port" --repo "$dir/overlay" \
        --dev-auth --gate none --allow-unvalidated --write > "$dir/console.log" 2>&1 &
    console_pid=$!

    for _ in $(seq 30); do
        curl -sf -o /dev/null "http://127.0.0.1:$port/devices" && break
        sleep 0.5
    done
    curl -sf -o /dev/null "http://127.0.0.1:$port/devices" || {
        echo "the console did not come up; last lines of $dir/console.log:" >&2
        tail -20 "$dir/console.log" >&2
        exit 1
    }

    # The simulator is what makes this a fleet rather than a list: devices
    # beat, converge slowly, go quiet, and a few report an error. -station
    # adds machines waiting on an imaging line, which is the part no
    # screenshot explains.
    "$dir/fleetsim" -fleet "$dir/overlay/fleet.json" -repo "$dir/overlay" \
        -url "http://127.0.0.1:$port" -token "$SEXTANT_CHECKIN_TOKEN" \
        -interval 5s -station st-1 -station-pool 4 > "$dir/fleetsim.log" 2>&1 &
    sim_pid=$!

    echo
    echo "  console:  http://127.0.0.1:$port"
    echo "  fleet:    $dir/overlay        logs: $dir/console.log, $dir/fleetsim.log"
    echo "  ctrl-c to stop and clean up"
    echo
    wait $console_pid

clean:
    rm -f sextant sxctl fleetsim coverage.out coverage.html

# What the FORGE thinks of HEAD. `just ci` passing locally is not the same
# thing, and assuming it was left CI red for twenty commits.
ci-status:
    scripts/ci-status.sh --watch
