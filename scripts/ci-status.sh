#!/usr/bin/env bash
# What forgejo thinks of a commit. Run it after every push.
#
# WHY THIS EXISTS. The pre-push hook runs `just ci` locally and that is not the
# same thing as CI passing: on 2026-08-03 twenty consecutive commits were red
# on the runner while every local check was green, and nobody noticed because
# nobody looked. The status endpoint needs no credentials, so there was never a
# reason not to.
#
#   scripts/ci-status.sh          the run for HEAD
#   scripts/ci-status.sh --watch  poll until it finishes
#
# Credentials, when present in ~/.netrc for the forge, also fetch WHICH STEP
# failed - the status alone tells you something is wrong, not what.
set -euo pipefail
API=https://forgejo.bb-open.com/api/v1/repos/bb-open/DAWO-Sextant/actions/tasks
WEB=https://forgejo.bb-open.com/bb-open/DAWO-Sextant/actions/runs
sha=$(git rev-parse HEAD)

run_for() {
  curl -sf --max-time 20 "$API?limit=30" \
    | jq -r --arg sha "$sha" '.workflow_runs[] | select(.head_sha == $sha)
        | "\(.status)\t\(.run_number)"' | head -1
}

deadline=$(( $(date +%s) + 900 ))
while :; do
  line=$(run_for || true)
  status=${line%%$'\t'*}; run=${line##*$'\t'}
  case "${status:-}" in
    success) echo "CI groen  ($sha -> run $run)"; exit 0 ;;
    failure|cancelled)
      echo "CI $status  ($sha -> run $run)"
      echo "  $WEB/$run"
      # Which step, when the netrc has a credential for the forge.
      if grep -q forgejo.bb-open.com ~/.netrc 2>/dev/null; then
        read -r u p < <(awk '{for(i=1;i<=NF;i++){if($i=="machine")m=$(i+1);
          if($i=="login"&&m=="forgejo.bb-open.com")U=$(i+1);
          if($i=="password"&&m=="forgejo.bb-open.com")P=$(i+1)}} END{print U,P}' ~/.netrc)
        curl -sL --max-time 30 -u "$u:$p" -X POST -H 'Content-Type: application/json' \
          "$WEB/$run/jobs/0/attempt/1" \
          | jq -r '.state.currentJob.steps[]? | select(.status=="failure")
              | "  gefaald: \(.summary) (na \(.duration))"' 2>/dev/null || true
      fi
      exit 1 ;;
    "") echo "no run for $sha (not picked up yet)" ;;
    *) echo "CI ${status}…" ;;
  esac
  [ "${1:-}" = "--watch" ] || exit 2
  [ "$(date +%s)" -lt "$deadline" ] || { echo "wachttijd verstreken"; exit 3; }
  sleep 20
done
