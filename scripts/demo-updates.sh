#!/usr/bin/env bash
# demo-updates.sh - a local updates-flow playground with a simulated fleet.
#
# Starts, all on this machine and torn down on Ctrl-C:
#   1. a throwaway postgres (podman) for the observed plane,
#   2. a seeded overlay repo (150 fake devices in 6 groups, fleetsim -gen),
#   3. the console in dev-auth mode with the gate off,
#   4. fleetsim: every device checks in and follows its rings/<group> branch
#      with a realistic mix (slow movers, silent drops, deploy errors).
#
# Then walk the real flow in the browser: Org -> Updates-beleid (test group
# ict-test, ladder 10,30,60) -> Updates -> roll out -> watch the waves move
# on /updates/rollout. New "releases" are just commits: run
#   git -C "$DEMO_DIR/overlay" commit -q --allow-empty -m "release"
# and start the next rollout.
set -euo pipefail

DEMO_DIR="${DEMO_DIR:-$(mktemp -d -t sextant-demo-XXXX)}"
ADDR="${ADDR:-127.0.0.1:8080}"
PG_PORT="${PG_PORT:-15432}"
TOKEN="demo-checkin-token"
DEVICES="${DEVICES:-150}"

say() { printf '\033[1;32m[demo]\033[0m %s\n' "$*"; }

cleanup() {
  say "opruimen..."
  [ -n "${SIM_PID:-}" ] && kill "$SIM_PID" 2>/dev/null || true
  [ -n "${CONSOLE_PID:-}" ] && kill "$CONSOLE_PID" 2>/dev/null || true
  podman rm -f sextant-demo-pg >/dev/null 2>&1 || true
}
trap cleanup EXIT

say "bouwen (console + fleetsim)..."
go build -o "$DEMO_DIR/sextant" ./cmd/sextant
go build -o "$DEMO_DIR/fleetsim" ./cmd/fleetsim

say "wegwerp-postgres starten op :$PG_PORT..."
podman rm -f sextant-demo-pg >/dev/null 2>&1 || true
podman run -d --name sextant-demo-pg -p "$PG_PORT:5432" \
  -e POSTGRES_PASSWORD=demo -e POSTGRES_DB=sextant \
  docker.io/library/postgres:16-alpine >/dev/null
for i in $(seq 1 30); do
  podman exec sextant-demo-pg pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done

say "overlay-repo seeden met $DEVICES fake devices..."
REPO="$DEMO_DIR/overlay"
mkdir -p "$REPO"
git -C "$REPO" init -q -b main
"$DEMO_DIR/fleetsim" -gen "$DEVICES" > "$REPO/fleet.json"
cp examples/overlay/catalog.json "$REPO/catalog.json" 2>/dev/null || echo '[]' > "$REPO/catalog.json"
git -C "$REPO" add . && git -C "$REPO" -c user.name=demo -c user.email=demo@local commit -q -m "seed demo fleet"

say "console starten op http://$ADDR (dev-auth, gate uit)..."
SEXTANT_REPO="$REPO" SEXTANT_GATE=none SEXTANT_ADDR="$ADDR" \
  SEXTANT_CHECKIN_TOKEN="$TOKEN" \
  SEXTANT_PG_DSN="postgres://postgres:demo@127.0.0.1:$PG_PORT/sextant?sslmode=disable" \
  SEXTANT_ORG_NAME="Demo Gemeente" \
  "$DEMO_DIR/sextant" --dev-auth &
CONSOLE_PID=$!
sleep 2

say "fleet-simulator starten ($DEVICES devices, beat 10s)..."
"$DEMO_DIR/fleetsim" -url "http://$ADDR" -token "$TOKEN" \
  -repo "$REPO" -fleet "$REPO/fleet.json" -interval 10s &
SIM_PID=$!

say ""
say "KLAAR. Open http://$ADDR"
say "  1. Org -> Updates-beleid: testgroep 'ict-test', ladder bv. 10, 30, 60"
say "  2. Nieuwe release maken:  git -C $REPO commit -q --allow-empty -m 'release'"
say "  3. Updates -> uitrollen -> volg de waves op /updates/rollout"
say "Ctrl-C ruimt alles op. DEMO_DIR=$DEMO_DIR"
wait "$CONSOLE_PID"
