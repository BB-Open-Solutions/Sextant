#!/usr/bin/env bash
# Vendor this repository's chart into a checkout of the Proxy charts repo.
#
# The marketplace wants apps/<name>/helm/ inside its own tree, so a copy is
# unavoidable. What is avoidable is a copy somebody edits: this script is the
# only supported way to produce it, and it refuses rather than guesses.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart="$here/../helm"
target="${1:-}"

if [ -z "$target" ]; then
  echo "usage: $(basename "$0") <path-to-charts-checkout>" >&2
  echo "  e.g. $(basename "$0") ../charts" >&2
  exit 2
fi
if [ ! -d "$target/apps" ]; then
  echo "$target does not look like the charts repo: no apps/ directory" >&2
  exit 1
fi

chart_version=$(awk '/^version:/ { print $2; exit }' "$chart/Chart.yaml")
listing_version=$(awk '/^version:/ { print $2; exit }' "$here/metadata.yaml")

# The listing version and the chart version are the same release or the
# marketplace offers a version that does not exist. Checking beats trusting:
# these two files sit in different directories and are edited months apart.
if [ "$chart_version" != "$listing_version" ]; then
  echo "refusing: chart is $chart_version, metadata.yaml says $listing_version" >&2
  echo "bump deploy/marketplace/metadata.yaml to match before syncing" >&2
  exit 1
fi

dest="$target/apps/sextant"
mkdir -p "$dest"
# Replace rather than merge: a file we deleted here must disappear there too,
# and rsync --delete is the only way that is true without thinking about it.
rm -rf "$dest/helm"
cp -r "$chart" "$dest/helm"
cp "$here/metadata.yaml" "$dest/metadata.yaml"

echo "synced sextant $chart_version into $dest"
echo
echo "next, in that checkout:"
echo "  git switch -c sextant-$chart_version"
echo "  git add apps/sextant && git commit"
echo "  # open a pull request; after it merges, tag on their main:"
echo "  git tag -a sextant-$chart_version -m 'sextant $chart_version'"
echo
echo "the listing version in the marketplace is the bare semver ($chart_version),"
echo "not the tag name."
