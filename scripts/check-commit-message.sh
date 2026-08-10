#!/usr/bin/env bash
# Validate one commit message against Conventional Commits.
#
# WHY THIS EXISTS. CONTRIBUTING.md has required Conventional Commits since the
# repository was opened, and on 2026-08-08 exactly one of the last forty
# commits followed it. A rule nobody checks is a preference, and this project
# already learned that lesson the expensive way: the coverage floor sat in CI
# for two weeks without running, printing a number nobody could have failed.
#
# So this is the check, and it runs in two places - the commit-msg hook, where
# the fix costs nothing, and CI, where it is the backstop.
#
# Usage: check-commit-message.sh <file>   (a file holding the message)
#        check-commit-message.sh -        (message on stdin)
set -euo pipefail

src="${1:-}"
if [ -z "$src" ]; then
  echo "usage: $0 <file|->" >&2
  exit 2
fi
if [ "$src" = "-" ]; then
  message=$(cat)
else
  message=$(cat "$src")
fi

# Strip comment lines: the hook is handed the editor's buffer, which carries
# git's own "# Please enter the commit message" block.
message=$(printf '%s\n' "$message" | grep -v '^#' || true)
subject=$(printf '%s\n' "$message" | head -1)

fail() {
  echo "commit message rejected: $1" >&2
  echo >&2
  echo "  subject: $subject" >&2
  echo >&2
  echo "  format:  <type>(<optional scope>): <description>" >&2
  echo "  types:   feat fix docs refactor chore test perf build ci style revert" >&2
  echo "  example: feat(rollout): halt alerting on failed health gates" >&2
  echo >&2
  echo "  A breaking change adds '!' before the colon and explains itself in" >&2
  echo "  the body. See CONTRIBUTING.md." >&2
  exit 1
}

# Machine-generated subjects are git's business, not ours. Rejecting them would
# break rebase and merge for no gain: none of them reach main as written.
case "$subject" in
  "Merge "*|"Revert "*|"fixup! "*|"squash! "*|"amend! "*)
    exit 0
    ;;
esac

[ -n "$subject" ] || fail "the subject is empty"

types='feat|fix|docs|refactor|chore|test|perf|build|ci|style|revert'
if ! printf '%s' "$subject" | grep -qE "^($types)(\([a-z0-9._/-]+\))?!?: .+"; then
  # Say which half is wrong; "does not match" sends people to the regex.
  if printf '%s' "$subject" | grep -qE '^[a-z]+(\([^)]*\))?!?: '; then
    got=$(printf '%s' "$subject" | sed -E 's/^([a-z]+).*/\1/')
    fail "'$got' is not one of the allowed types"
  fi
  fail "no '<type>: ' prefix"
fi

# 72 keeps `git log --oneline` readable in a terminal. Measured on 2026-08-08:
# fourteen of the previous forty subjects were longer, which is the argument
# for the limit rather than against it.
len=${#subject}
if [ "$len" -gt 72 ]; then
  fail "the subject is $len characters; the limit is 72"
fi

case "$subject" in
  *.) fail "the subject ends in a full stop" ;;
esac

# The description starts lowercase: it continues the type, it does not open a
# sentence. Proper nouns are the exception and are not worth a dictionary, so
# only a leading capital followed by lowercase is refused - "LDAPS" passes.
desc=${subject#*: }
if printf '%s' "$desc" | grep -qE '^[A-Z][a-z]'; then
  # Proper nouns trip this - "Codeberg is ...", "Postgres now ...". The rule
  # is still worth keeping (it catches sentence-style subjects), so the
  # message names the way out rather than leaving you to guess it: start with
  # a verb and the noun moves along with it, which usually reads better
  # anyway. Twice on 2026-08-10 the rewrite improved the subject.
  fail "the description starts with a capital (start with a verb, or move the proper noun later in the line)"
fi

# A body has to be separated from the subject, or git treats the whole thing as
# one long subject and every tool downstream inherits the mistake.
second=$(printf '%s\n' "$message" | sed -n '2p')
if [ -n "$second" ]; then
  fail "line 2 is not blank; a body needs an empty line after the subject"
fi

exit 0
