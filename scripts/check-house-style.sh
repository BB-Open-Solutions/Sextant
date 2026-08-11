#!/usr/bin/env bash
# Enforce the mechanical half of CLAUDE.md's writing rules.
#
# WHY THIS EXISTS. The rules in CLAUDE.md were all written down before, in
# CONTRIBUTING.md, and followed by almost nobody. This project keeps relearning
# that a rule nobody checks is a preference: Conventional Commits went forty
# commits unobserved, the coverage floor ran for two weeks printing a number
# nobody could fail, and "issue first" lost to ten commits in one afternoon.
#
# So the parts a script CAN judge are judged here. Taste is not in scope: no
# check for slop, for why-not-what comments, or for whether a claim carries its
# measurement. Those stay human, and CLAUDE.md says so.
#
# CHANGED LINES ONLY. The repository holds 324 em dashes written before this
# rule existed. A check that fails on all of them is a check somebody disables
# on its first run, so this reads the diff, not the tree. Old text is left for
# whoever next edits that paragraph.
#
# Usage:
#   scripts/check-house-style.sh              # staged changes (the hook)
#   scripts/check-house-style.sh <base>       # everything since <base> (CI)
#   scripts/check-house-style.sh --all        # the whole tree, for a cleanup
set -euo pipefail

mode="${1:-staged}"
case "$mode" in
  staged) diff_cmd=(git diff --cached --unified=0) ;;
  --all)  diff_cmd=(git diff --unified=0 "$(git hash-object -t tree /dev/null)") ;;
  *)      diff_cmd=(git diff --unified=0 "${mode}...HEAD") ;;
esac

fails=0
report() {
  printf 'house style: %s\n' "$1" >&2
  printf '  %s\n' "$2" >&2
  fails=$((fails + 1))
}

# Only added lines, and only in text we author. Generated files and vendored
# text are not ours to restyle.
raw=$("${diff_cmd[@]}" -- \
  '*.md' '*.go' '*.rs' '*.nix' '*.sh' '*.json' \
  ':(exclude)*/testdata/*' ':(exclude)agent/tests/fixtures/*' \
  ':(exclude)flake.lock' ':(exclude)go.sum' || true)

# Bracket expressions, not backslash-plus. ugrep - which is `grep` on at least
# one developer's machine - rejects `^\+\+\+` outright, and the error went to
# stderr where `|| true` swallowed it. The variable came back empty and this
# script announced that everything was clean while looking at an em dash.
#
# That is the failure CLAUDE.md calls "no silent caps", produced by the script
# meant to enforce it, on its first run. Hence the guard below: an empty read
# from a non-empty diff is an error, never a pass.
added=$(printf '%s\n' "$raw" | grep -E '^[+]' | grep -vE '^[+][+][+]' | sed 's/^+//' || true)

if [ -z "$raw" ]; then
  echo "house style: nothing added to check"
  exit 0
fi
if [ -z "$added" ]; then
  echo "house style: the diff was not empty but no added lines were read." >&2
  echo "  That means this check is broken, not that the text is clean." >&2
  exit 1
fi

# An em dash (U+2014) and its cousin the en dash used as a dash (U+2013),
# written as escapes so this script passes the rule it enforces. A checker
# exempted from its own rule is the first place the rule stops being true.
# CLAUDE.md: use a comma, a colon, a full stop, or two sentences.
dashes=$'\u2014\u2013'
if bad=$(printf '%s\n' "$added" | grep -n "[$dashes]" || true); [ -n "$bad" ]; then
  report "em or en dash in added text; use a comma, a colon, a full stop, or two sentences" \
    "$(printf '%s\n' "$bad" | head -5)"
fi

# Exclamation marks outside code. "!=" , "!" as Nix/Go negation and shell
# history are not punctuation, so this looks for one after a word or a space,
# which is how it appears in prose.
if bad=$(printf '%s\n' "$added" | grep -nE '[[:alnum:]] ?!(\s|$)' || true); [ -n "$bad" ]; then
  report "exclamation mark in prose; the sentence has to carry it alone" \
    "$(printf '%s\n' "$bad" | head -5)"
fi

# Ellipses standing in for a thought the writer did not finish.
#
# Matched by SHAPE rather than by an exclusion list: a word, then the dots,
# then the end of the line or a space. That is prose trailing off. It leaves
# alone the two legitimate uses, which an exclusion list kept missing - path
# elision inside a quoted error (`path:/.../DAWO-Sextant`, preceded by a
# slash) and Go variadics (`fmt.Println(a...)`, followed by a bracket).
if bad=$(printf '%s\n' "$added" | grep -nE '[[:alnum:]](\.\.\.|…)([[:space:]]|$)' || true); [ -n "$bad" ]; then
  report "ellipsis in prose; finish the sentence or cut it" \
    "$(printf '%s\n' "$bad" | head -5)"
fi

if [ "$fails" -gt 0 ]; then
  echo >&2
  echo "  $fails rule(s) broken. See CLAUDE.md, section Writing." >&2
  echo "  Deliberate exception: SKIP_HOUSE_STYLE=1 git commit ..." >&2
  exit 1
fi

echo "house style: added text is clean"
