#!/usr/bin/env bash
# Create the public project's labels and milestones on Codeberg.
#
# WHY THIS EXISTS. ADR 0024 says Codeberg is run as a public project rather
# than pushed to as a mirror, and the first thing that means is that somebody
# arriving finds labels and milestones instead of an empty repository.
#
# It is a script rather than a click-through for two reasons. The label set
# becomes reviewable in git, so a change to it is a change somebody can see.
# And the token stays with the person running it - it is read from the
# environment, never passed on a command line, and never written down.
#
# IDEMPOTENT. It creates what is missing and leaves the rest alone. It never
# deletes or renames: a label somebody added by hand is theirs, not ours to
# tidy away.
#
# Usage:
#   CODEBERG_TOKEN=... scripts/codeberg-project-setup.sh [--dry-run]
#
# The token needs the `issue` and `repository` scopes. Make it at
# https://codeberg.org/user/settings/applications, and delete it afterwards -
# this runs once in a while, not continuously.
set -euo pipefail

owner_repo="${CODEBERG_REPO:-DAWO/DAWO-Sextant}"
api="https://codeberg.org/api/v1/repos/${owner_repo}"
dry=false
[ "${1:-}" = "--dry-run" ] && dry=true

if [ "$dry" = false ] && [ -z "${CODEBERG_TOKEN:-}" ]; then
  echo "CODEBERG_TOKEN is not set. Run with --dry-run to see what it would do." >&2
  exit 2
fi

# name|colour|description
# Kept deliberately short. More labels than a person remembers is worse than
# none: the ones nobody applies make the ones that matter unreliable.
labels='bug|d73a4a|Something does not do what it should
enhancement|a2eeef|A capability, or a different way of working
documentation|0075ca|The text is wrong, missing, or drifted from the code
security|b60205|Publicly discussable. Real reports go to SECURITY.md, not here
good first issue|7057ff|Small, bounded, and does not need a fleet to test
help wanted|008672|We know what is needed and are not getting to it
needs-decision|fbca04|Waiting on a decision rather than on work
upstream|5319e7|Belongs in DAWO-NixOS or nixpkgs, not in this repository'

# title|description
# Only the near ones. A milestone for a distant release with nothing in it
# reads as an abandoned project rather than a plan.
# Naming follows DAWO-Core, the sibling project already running on Codeberg:
# a v-prefixed release milestone, plus Backlog and Ongoing for the work that
# is not a release. Two projects on the same forge should not invent two
# conventions for the same thing.
milestones='v1.0.0|Production at a Dutch municipality. Scope and gate: docs/1.0-fit-gap.md
v1.1.0|What the first non-pilot machines hit: multiple drives, app profiles, admin devices as a named class, the Wazuh agent on NixOS. See docs/roadmap.md
v1.2.0|Governance a municipality asks for: capability RBAC on directory groups, four-eyes narrowed to where it earns its place, per-item compliance results. See docs/roadmap.md
Backlog|Anything not added to a milestone, a nice to pick and choose next issues from.
Ongoing|Items that are ongoing and have no defined end.'

# curl exits 0 on HTTP 4xx unless told otherwise. The first run reported
# "created" for twenty issues while Codeberg had accepted thirteen and
# rate-limited the rest, and the run after that repeated the lie because this
# fix had not been committed yet. --fail-with-body makes a refusal a failure.
call() {
  curl -sS --fail-with-body -X "$1" "$2" \
    -H "Authorization: token ${CODEBERG_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$3"
}

existing_labels=$(curl -sS "${api}/labels?limit=100" | grep -o '"name":"[^"]*"' | cut -d'"' -f4 || true)
existing_ms=$(curl -sS "${api}/milestones?limit=100&state=all" | grep -o '"title":"[^"]*"' | cut -d'"' -f4 || true)

echo "== labels =="
while IFS='|' read -r name colour desc; do
  [ -z "$name" ] && continue
  if printf '%s\n' "$existing_labels" | grep -Fxq "$name"; then
    echo "  exists   $name"
    continue
  fi
  if [ "$dry" = true ]; then
    echo "  would add $name  (#$colour)"
    continue
  fi
  body=$(printf '{"name":%s,"color":"#%s","description":%s}' \
    "$(printf '%s' "$name" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read()))')" \
    "$colour" \
    "$(printf '%s' "$desc" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read()))')")
  if call POST "${api}/labels" "$body" >/dev/null; then
    echo "  created  $name"
  else
    echo "  FAILED   $name" >&2
  fi
done <<< "$labels"

echo "== milestones =="
while IFS='|' read -r title desc; do
  [ -z "$title" ] && continue
  if printf '%s\n' "$existing_ms" | grep -Fxq "$title"; then
    echo "  exists   $title"
    continue
  fi
  if [ "$dry" = true ]; then
    echo "  would add $title"
    continue
  fi
  body=$(printf '{"title":%s,"description":%s}' \
    "$(printf '%s' "$title" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read()))')" \
    "$(printf '%s' "$desc" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read()))')")
  if call POST "${api}/milestones" "$body" >/dev/null; then
    echo "  created  $title"
  else
    echo "  FAILED   $title" >&2
  fi
done <<< "$milestones"

# Issues come from docs/project/issues.json so the wording gets the same
# review as anything else in the repository, and so the set is reproducible.
# Matched by TITLE: an issue whose title already exists is left alone, never
# edited and never closed. Once an issue is open it belongs to the discussion
# on it rather than to a file.
echo "== issues =="
issues_file="$(dirname "$0")/../docs/project/issues.json"
if [ ! -f "$issues_file" ]; then
  echo "  no $issues_file; skipping" >&2
else
  existing_titles=$(curl -sS "${api}/issues?state=all&limit=100" \
    | python3 -c 'import json,sys
try: print("\n".join(i["title"] for i in json.load(sys.stdin)))
except Exception: pass')
  ms_map=$(curl -sS "${api}/milestones?limit=100&state=all")

  count=$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))["issues"]))' "$issues_file")
  i=0
  while [ "$i" -lt "$count" ]; do
    title=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["issues"][int(sys.argv[2])]["title"])' "$issues_file" "$i")
    if printf '%s\n' "$existing_titles" | grep -Fxq "$title"; then
      echo "  exists   $title"
      i=$((i+1)); continue
    fi
    if [ "$dry" = true ]; then
      echo "  would add $title"
      i=$((i+1)); continue
    fi
    body=$(MS="$ms_map" python3 -c '
import json, os, sys
issue = json.load(open(sys.argv[1]))["issues"][int(sys.argv[2])]
out = {"title": issue["title"], "body": issue["body"], "labels": []}
# Milestone by id: the API takes the number, not the name.
for m in json.loads(os.environ["MS"]):
    if m["title"] == issue.get("milestone"):
        out["milestone"] = m["id"]
print(json.dumps(out))' "$issues_file" "$i")
    if call POST "${api}/issues" "$body" >/dev/null; then
      echo "  created  $title"
      :  # labels are applied in a pass of their own, below
    else
      echo "  FAILED   $title" >&2
    fi
    i=$((i+1))
  done
fi


# Labels, in a pass of its own after every issue exists. The create endpoint
# wants label IDs rather than names, and resolving them inline would have made
# one failure lose the issue body too.
#
# Applied only where an issue currently has none: a label somebody added or
# removed by hand is a decision, not drift for a script to correct.
echo "== labels on issues =="
if [ -f "$issues_file" ]; then
  live_labels=$(curl -sS "${api}/labels?limit=100")
  live_issues=$(curl -sS "${api}/issues?state=all&limit=100")
  plan=$(LBL="$live_labels" ISS="$live_issues" python3 - "$issues_file" <<'PYEOF'
import json, os, sys
want = json.load(open(sys.argv[1]))["issues"]
live = {i["title"]: i for i in json.loads(os.environ["ISS"])}
ids  = {l["name"]: l["id"] for l in json.loads(os.environ["LBL"])}
for w in want:
    here = live.get(w["title"])
    if here is None or here.get("labels"):
        continue
    got = [ids[n] for n in (w.get("labels") or []) if n in ids]
    if got:
        print(here["number"], ",".join(str(g) for g in got))
PYEOF
)
  if [ -z "$plan" ]; then
    echo "  nothing to label"
  else
    while read -r num idlist; do
      [ -z "$num" ] && continue
      if [ "$dry" = true ]; then
        echo "  would label #$num"
        continue
      fi
      body="{\"labels\":[${idlist}]}"
      if call POST "${api}/issues/${num}/labels" "$body" >/dev/null 2>&1; then
        echo "  labelled #$num"
      else
        echo "  FAILED to label #$num" >&2
      fi
    done <<< "$plan"
  fi
fi

echo
echo "Not done by this script, because they are repository settings rather than"
echo "content, and they are the two a visitor notices first:"
echo "  - Topics. Currently none, which is why nobody finds this by searching."
echo "    Suggested: nixos nix fleet-management device-management gitops"
echo "               public-sector self-hosted golang eupl"
echo "  - Website. Currently empty; docs.sextantfleet.com exists."
