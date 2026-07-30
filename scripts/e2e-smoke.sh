#!/usr/bin/env bash
# Console-side smoke for the acceptance runs (docs/e2e-acceptance-plan.md).
#
# This covers the part a machine can check faster and more honestly than a
# human clicking: that every page answers, that it answers with a WHOLE
# document, and that the console reports the build you think you are testing.
# Everything else in the plan needs eyes and hardware.
#
# The whole-document check is not padding. render() turns a template error
# into a 500, but a page can also answer 200 with half a document when a
# template fails midway - which is exactly what a person skimming for "does it
# load" will pass. Any page that does not end in </html> is truncated.
#
# Usage:
#   scripts/e2e-smoke.sh https://sextant.bb-open.com [expected-version]
#
# Authentication: the console is behind OIDC, so pages answer 302 to the login
# unless you pass a session. Export SEXTANT_COOKIE with a browser session
# cookie to check them as a logged-in user:
#   SEXTANT_COOKIE='session=...' scripts/e2e-smoke.sh https://sextant.bb-open.com
# Without it the script still proves the console is up, serving, and on the
# expected build, and it says plainly that the pages were not checked rather
# than reporting a 302 as a pass.
set -uo pipefail

BASE="${1:?usage: e2e-smoke.sh <base-url> [expected-version]}"
BASE="${BASE%/}"
WANT_VERSION="${2:-}"

pass=0 fail=0 skip=0
ok()   { printf '  ok    %s\n' "$*"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$*"; fail=$((fail+1)); }
none() { printf '  skip  %s\n' "$*"; skip=$((skip+1)); }

curl_args=(--silent --show-error --max-time 20)
[ -n "${SEXTANT_COOKIE:-}" ] && curl_args+=(--cookie "$SEXTANT_COOKIE")

echo "== console: $BASE"

# 1. Reachability and build identity. Deliberately first: every later result is
#    meaningless if this is the wrong version, and that has happened - flux can
#    report a successful rollout while the previous pod keeps serving.
code=$(curl "${curl_args[@]}" -o /dev/null -w '%{http_code}' "$BASE/healthz" 2>/dev/null)
case "$code" in
  200) ok "healthz" ;;
  000) bad "healthz: unreachable"; echo; echo "console not reachable; nothing else can be judged"; exit 1 ;;
  *)   bad "healthz: HTTP $code" ;;
esac

metrics=$(curl "${curl_args[@]}" "$BASE/metrics" 2>/dev/null)
version=$(printf '%s' "$metrics" | sed -n 's/.*sextant_build_info{[^}]*version="\([^"]*\)".*/\1/p' | head -1)
if [ -z "$version" ]; then
  none "build identity: /metrics not exposed publicly (check /status by hand)"
elif [ -n "$WANT_VERSION" ] && [ "$version" != "$WANT_VERSION" ]; then
  bad "build identity: serving $version, expected $WANT_VERSION"
else
  ok "build identity: $version"
fi

# 2. Pages. Same list the render smoke test uses, so the two cannot drift.
PAGES=(
  / /devices /groups /settings /policies /compliance /changes
  /updates /updates/rollout /org/updates /access /audit /profile
  /station /enroll /integrations /overlays /secrets /service-accounts
  /notifications /org /mail /status
)

echo "== pages"
if [ -z "${SEXTANT_COOKIE:-}" ]; then
  none "pages: no SEXTANT_COOKIE, so a redirect to login cannot be told from a working page"
else
  for p in "${PAGES[@]}"; do
    body=$(curl "${curl_args[@]}" -w '\n%{http_code}' "$BASE$p" 2>/dev/null)
    code="${body##*$'\n'}"
    html="${body%$'\n'*}"
    case "$code" in
      200)
        if printf '%s' "$html" | grep -q '</html>'; then
          ok "$p"
        else
          bad "$p: 200 but the document is truncated ($(printf '%s' "$html" | wc -c) bytes)"
        fi ;;
      302|303) bad "$p: redirected ($code) - the session cookie is not being accepted" ;;
      *)       bad "$p: HTTP $code" ;;
    esac
  done
fi

# 3. The public API surface. No session needed, and worth its own check: the
#    OpenAPI document is what an integrator reads first.
echo "== api"
code=$(curl "${curl_args[@]}" -o /dev/null -w '%{http_code}' "$BASE/api/v1/openapi.json")
if [ "$code" = "200" ]; then
  ok "openapi document"
else
  bad "openapi document: HTTP $code"
fi

# An unauthenticated check-in must be refused - the check-in endpoint is the
# fleet's front door, and "it accepted my request" is not the answer you want.
#
# Off by default, because the test is only safe where its own failure is safe:
# if the endpoint DOES accept it, a device called smoke-test now exists in the
# fleet you were about to run an acceptance round against. Enable it against a
# test console, not production.
if [ "${SEXTANT_PROBE_CHECKIN:-0}" = "1" ]; then
  code=$(curl "${curl_args[@]}" -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' --data '{"tag":"smoke-test"}' "$BASE/api/checkin")
  case "$code" in
    401|403) ok "unauthenticated check-in refused ($code)" ;;
    200|201|202) bad "unauthenticated check-in ACCEPTED ($code) - anyone can invent a device, and smoke-test may now exist" ;;
    *) none "unauthenticated check-in: HTTP $code (neither accepted nor a clean refusal)" ;;
  esac
else
  none "unauthenticated check-in not probed (set SEXTANT_PROBE_CHECKIN=1 on a test console)"
fi

echo
printf 'passed %d, failed %d, skipped %d\n' "$pass" "$fail" "$skip"
[ "$skip" -gt 0 ] && echo "skipped checks are NOT passes - see the notes above"
[ "$fail" -eq 0 ]
