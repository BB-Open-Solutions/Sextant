#!/usr/bin/env bash
# Regenerate the subset Material Symbols web font.
#
# The console self-hosts a SUBSET of Material Symbols Outlined: only the icons
# actually used ship, keeping the woff2 small. Icons enter two ways:
#   1. Statically in templates, as the ligature text inside a
#      <span class="material-symbols-outlined">name</span>.
#   2. Dynamically from Go, where the icon name is a string literal chosen at
#      render time (integration cards, notification kinds). Those cannot be
#      grepped reliably, so they are listed in EXTRA below - keep it in sync
#      when you add a Go-chosen icon.
#
# Run after adding any icon:
#   scripts/regen-icon-font.sh
# Requires nix (pulls fonttools + brotli + the full font). Deterministic:
# same icon set in, same bytes out.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=internal/http/web/static/fonts/material-symbols.woff2
TMPL=internal/http/web/templates

# Icons chosen in Go code, not visible to the template grep. Keep sorted.
EXTRA="badge delete_forever gpp_bad security vpn_lock"

# Pull every ligature name out of the templates, add the Go-chosen ones,
# de-duplicate, and join with spaces as the pyftsubset --text input. The
# ligature-substitution closure then keeps each composite glyph because all of
# its component letters are in the text.
mapfile -t names < <(
  { grep -rhoE 'material-symbols-outlined[^>]*>[a-z0-9_]+<' "$TMPL" \
      | grep -oE '>[a-z0-9_]+<' | tr -d '><'
    printf '%s\n' $EXTRA
  } | sort -u
)
text="${names[*]}"
echo "subsetting $(printf '%s\n' "${names[@]}" | wc -l) icons"

src="$(nix build --no-link --print-out-paths 'nixpkgs#material-symbols')/share/fonts/truetype/MaterialSymbolsOutlined[FILL,GRAD,opsz,wght].ttf"

# One python env with BOTH fonttools and brotli so woff2 compression works
# (pyftsubset --flavor=woff2 imports brotli at runtime; separate store paths
# would not share sys.path).
pyenv='let p = (builtins.getFlake "nixpkgs").legacyPackages.${builtins.currentSystem};
       in p.python3.withPackages (ps: [ ps.fonttools ps.brotli ])'

# The upstream file is a variable font (FILL,GRAD,opsz,wght). The console
# renders one fixed style, and carrying gvar deltas for every axis bloats the
# subset ~7x, so pin the axes to a single static instance BEFORE subsetting.
inst="$(mktemp --suffix=.ttf)"
trap 'rm -f "$inst"' EXIT

nix shell --impure --expr "$pyenv" --command bash -c '
  set -e
  fonttools varLib.instancer "$1" wght=400 FILL=0 GRAD=0 opsz=24 \
    --output="$2" >/dev/null
  pyftsubset "$2" \
    --text="$3" \
    --layout-features="rlig" \
    --flavor=woff2 \
    --output-file="$4"
' _ "$src" "$inst" "$text" "$OUT"

echo "wrote $OUT ($(stat -c%s "$OUT") bytes)"
