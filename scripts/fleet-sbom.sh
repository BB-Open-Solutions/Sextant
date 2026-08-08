#!/usr/bin/env bash
# An SBOM and a vulnerability report for what the fleet actually runs.
#
# WHY THIS EXISTS. Audit finding S3: the release pipeline produces an SBOM per
# container image, and a municipality does not run our images - it runs a NixOS
# closure on a laptop. The image SBOM says nothing about the attack surface
# that matters.
#
# The answer is not to discover the inventory but to export it. A closure is an
# exact dependency graph, which is more than most SBOM tooling can infer, and
# nix packages carry upstream version numbers - the same ones NVD indexes,
# unlike a distribution's backport revisions.
#
# It also answers the CVE gap recorded in docs/compliance/iso27002-mapping.md
# (ISO 27002 8.8). Wazuh cannot: its vulnerability detector reads a dpkg or rpm
# inventory that NixOS does not have, and its matcher is fed by vendor advisory
# streams that nixpkgs does not publish. Scanning the closure off-device is not
# a workaround for that - it is the better answer, because you scan once per
# distinct configuration rather than once per machine.
#
# Usage: fleet-sbom.sh <flakeref> [outdir] [-hosts a,b,c]
#
#   fleet-sbom.sh . out
#   fleet-sbom.sh github:bb-open/overlay out -hosts e2e5
set -euo pipefail

flakeref="${1:-}"
outdir="${2:-fleet-sbom}"
hosts_arg=""
shift 2 2>/dev/null || shift $#
while [ $# -gt 0 ]; do
  case "$1" in
    -hosts|--hosts) hosts_arg="${2:-}"; shift 2 ;;
    *) echo "fleet-sbom: unknown argument $1" >&2; exit 2 ;;
  esac
done
if [ -z "$flakeref" ]; then
  echo "usage: fleet-sbom.sh <flakeref> [outdir] [-hosts a,b,c]" >&2
  exit 2
fi

for bin in nix sbomnix vulnxscan jq; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "fleet-sbom: $bin is not on PATH" >&2
    exit 2
  }
done

mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)

hosts=()
if [ -n "$hosts_arg" ]; then
  IFS=',' read -r -a hosts <<< "$hosts_arg"
else
  mapfile -t hosts < <(nix eval --json "${flakeref}#nixosConfigurations" \
    --apply builtins.attrNames | jq -r '.[]')
fi
[ ${#hosts[@]} -gt 0 ] || {
  echo "fleet-sbom: the flake exposes no nixosConfigurations" >&2
  exit 1
}

echo "fleet-sbom: ${#hosts[@]} host(s) in $flakeref"

# Build first, and keep a GC root per closure. Without one the store path can
# be collected between the build and the scan - observed on a developer machine
# on 2026-08-08, where the derivation vanished mid-session and the scan failed
# with "is not a valid store path".
roots="$outdir/.gcroots"
mkdir -p "$roots"

declare -A closure_of=()   # host -> store path
declare -A hosts_of=()     # store path -> space-separated hosts
for host in "${hosts[@]}"; do
  echo "fleet-sbom: building $host"
  link="$roots/$host"
  nix build --no-warn-dirty --out-link "$link" \
    "${flakeref}#nixosConfigurations.${host}.config.system.build.toplevel"
  path=$(readlink -f "$link")
  closure_of["$host"]="$path"
  hosts_of["$path"]="${hosts_of[$path]:-} $host"
done

# One scan per DISTINCT closure. A fleet has many devices and few
# configurations; scanning per device would multiply the same answer and make
# the run look thorough while adding nothing.
mapfile -t distinct < <(printf '%s\n' "${closure_of[@]}" | sort -u)
echo "fleet-sbom: ${#distinct[@]} distinct closure(s)"

manifest="$outdir/manifest.json"
: > "$outdir/.entries"
rc=0
for path in "${distinct[@]}"; do
  # The store hash is the stable name: two runs of the same configuration
  # produce the same file name, so a report can be diffed against the last one.
  id=$(basename "$path" | cut -d- -f1)
  name=$(basename "$path" | cut -d- -f2-)
  echo "fleet-sbom: scanning $name ($id)"

  sbomnix --cdx "$outdir/sbom-$id.cdx.json" \
          --spdx "$outdir/sbom-$id.spdx.json" \
          --csv  "$outdir/sbom-$id.csv" "$path" \
    || { echo "fleet-sbom: sbomnix failed for $path" >&2; rc=1; continue; }

  # Scanned against the CLOSURE rather than the SBOM we just wrote, because
  # vulnxscan skips its vulnix pass on an SBOM input and vulnix is the
  # nix-native scanner of the three. The SBOM is still the durable artifact:
  # `vulnxscan --sbom sbom-<id>.cdx.json` re-scans a release months later,
  # against a feed that did not exist when it shipped, without the closure
  # being present or rebuildable. Keep the SBOMs for exactly that.
  #
  # vulnxscan cross-references vulnix, grype and OSV against the same closure.
  # Its exit status is NOT the script's: a report that breaks the build gets
  # switched off within a month, and then there is no report at all. This
  # reports; deciding what an open CVE blocks is a policy question and belongs
  # to whoever reads it.
  # A failed scan and a clean scan must never look alike. Observed on
  # 2026-08-08: grype could not download its vulnerability database (the disk
  # was full), vulnxscan died, and an earlier version of this script recorded
  # "0 findings" - a report that reads as "nothing wrong with the fleet" when
  # it means "nobody looked". That is the same false negative this whole tool
  # exists to replace, so it is recorded as an absence of data, and it does
  # fail the run: findings are somebody's decision, a scan that did not
  # happen is a broken pipeline.
  scanned=true
  if ! vulnxscan --out "$outdir/vulns-$id.csv" "$path"; then
    echo "fleet-sbom: the vulnerability scan FAILED for $name - this is not a clean result" >&2
    scanned=false
    rc=1
  fi

  if [ "$scanned" = true ] && [ -f "$outdir/vulns-$id.csv" ]; then
    found=$(( $(wc -l < "$outdir/vulns-$id.csv") - 1 ))
    [ "$found" -lt 0 ] && found=0
    count_json="$found"
  else
    scanned=false
    count_json=null
  fi

  jq -n --arg id "$id" --arg path "$path" --arg name "$name" \
        --arg hosts "$(echo "${hosts_of[$path]}" | xargs)" \
        --argjson count "$count_json" --argjson scanned "$scanned" \
    '{id:$id, closure:$path, name:$name,
      hosts:($hosts|split(" ")),
      sbom:{cdx:("sbom-"+$id+".cdx.json"), spdx:("sbom-"+$id+".spdx.json")},
      vulnerabilities:{report:("vulns-"+$id+".csv"), scanned:$scanned, count:$count}}' \
    >> "$outdir/.entries"
done

jq -s --arg ref "$flakeref" '{flakeref:$ref, closures:.}' \
  "$outdir/.entries" > "$manifest"
rm -f "$outdir/.entries"

echo
echo "fleet-sbom: wrote $manifest"
jq -r '.closures[] | "  \(.name): " +
       (if .vulnerabilities.scanned
        then "\(.vulnerabilities.count) finding(s)"
        else "SCAN FAILED - no vulnerability data" end) +
       ", hosts: \(.hosts|join(", "))"' "$manifest"
if [ "$rc" -ne 0 ]; then
  echo
  echo "fleet-sbom: at least one step failed; the report above is incomplete" >&2
fi
exit $rc
