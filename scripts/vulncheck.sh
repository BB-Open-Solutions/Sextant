#!/usr/bin/env bash
# vulncheck.sh - the vulnerability gate, with an explicit exception list.
#
# govulncheck is blocking by design: a new advisory whose vulnerable code we
# actually CALL stops the line. But "stop the line" needs an audited escape
# valve for the case where no fixed toolchain exists yet: an id listed in
# .govulncheck-exceptions (with a written reason and revisit condition) is
# accepted; anything else still fails. Runs on the devshell's pinned go, so
# the scan judges the exact toolchain the release binaries are built with.
set -uo pipefail
cd "$(dirname "$0")/.."

out=$(CGO_ENABLED=0 govulncheck ./... 2>&1)
rc=$?
printf '%s\n' "$out"
[ "$rc" -eq 0 ] && exit 0

# Non-3 exits are tool errors (bad patterns, no go), never acceptable.
if [ "$rc" -ne 3 ]; then
  echo "vulncheck: govulncheck failed (exit $rc), not a findings exit" >&2
  exit "$rc"
fi

# Called vulnerabilities are reported as "Vulnerability #N: GO-XXXX-NNNN".
ids=$(printf '%s\n' "$out" | sed -n 's/^Vulnerability #[0-9]*: \(GO-[0-9]*-[0-9]*\).*/\1/p' | sort -u)
if [ -z "$ids" ]; then
  echo "vulncheck: findings exit but no vulnerability ids parsed - failing closed" >&2
  exit 1
fi

bad=0
for id in $ids; do
  if grep -qE "^$id([[:space:]]|$)" .govulncheck-exceptions 2>/dev/null; then
    echo "vulncheck: $id accepted via .govulncheck-exceptions (revisit condition applies)"
  else
    echo "vulncheck: BLOCKING - $id is called and not excepted" >&2
    bad=1
  fi
done
exit "$bad"
