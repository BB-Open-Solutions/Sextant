#!/usr/bin/env bash
# rekey-secrets.sh - re-encrypt an overlay's agenix secrets for every device
# the console knows a host key for.
#
# WHY THIS EXISTS
#   A freshly imaged device holds its own SSH host keypair. agenix encrypts
#   each secret for a fixed set of recipients, so until the device's public
#   key is one of them, every secret on that device fails to decrypt: the
#   activation script exits non-zero, comin refuses to switch, and the device
#   stays frozen at its image-time generation forever.
#
# THE WORKFLOW
#   1. Enroll the device and image it. The imaging station pre-seeds the host
#      keypair and reports the PUBLIC key with its install status.
#   2. The console records it on the device (visible on the device page as
#      "Host key recorded (sha256:...)").
#   3. Run this script against the overlay checkout. It fetches every recorded
#      host key and re-encrypts secrets/*.age for the admin identity plus all
#      of them.
#   4. Review `git diff --stat` in the overlay and commit. The fleet picks the
#      new ciphertexts up on the next converge.
#
# USAGE
#   SEXTANT_URL=https://console.example.org \
#   SEXTANT_API_TOKEN=... \
#     scripts/rekey-secrets.sh -i ~/.age/admin.key -s ../overlay/secrets
#
#   -i, --identity PATH   admin age identity (age or SSH private key). Its
#                         public key is always a recipient, so the operator
#                         never locks themselves out. REQUIRED.
#   -s, --secrets  DIR    the overlay's secrets directory (*.age). REQUIRED.
#   -r, --recipients FILE extra recipients, one per line (other admins, a
#                         break-glass key). Optional, repeatable.
#   -n, --dry-run         report what would change; write nothing.
#   -f, --force           rekey every file even when already up to date.
#
# IDEMPOTENCE
#   The recipient set is hashed and recorded per file in secrets/.rekey-state
#   (public data: a hash and file names). A rerun with an unchanged recipient
#   set does nothing. Enroll a device, rerun, and only then does every file
#   change. --force overrides.
#
# SAFETY
#   Plaintext exists only inside a private temp dir that is removed on exit,
#   and is never printed - not on success, not in an error. Every file is
#   decrypted BEFORE any file is rewritten, so a secret the admin identity
#   cannot open aborts the run with nothing touched.

set -euo pipefail

die() { printf 'rekey-secrets: %s\n' "$*" >&2; exit 1; }
note() { printf 'rekey-secrets: %s\n' "$*"; }

IDENTITY=""
SECRETS_DIR=""
EXTRA_RECIPIENT_FILES=()
DRY_RUN=0
FORCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    -i|--identity)   IDENTITY="${2:-}"; shift 2 ;;
    -s|--secrets)    SECRETS_DIR="${2:-}"; shift 2 ;;
    -r|--recipients) EXTRA_RECIPIENT_FILES+=("${2:-}"); shift 2 ;;
    -n|--dry-run)    DRY_RUN=1; shift ;;
    -f|--force)      FORCE=1; shift ;;
    -h|--help)       sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)               die "unknown argument $1 (try --help)" ;;
  esac
done

[ -n "$IDENTITY" ]    || die "no admin identity (-i); refusing to produce secrets you cannot read"
[ -r "$IDENTITY" ]    || die "admin identity $IDENTITY is not readable"
[ -n "$SECRETS_DIR" ] || die "no secrets directory (-s)"
[ -d "$SECRETS_DIR" ] || die "secrets directory $SECRETS_DIR does not exist"
[ -n "${SEXTANT_URL:-}" ]       || die "SEXTANT_URL is not set (the console base URL)"
[ -n "${SEXTANT_API_TOKEN:-}" ] || die "SEXTANT_API_TOKEN is not set"

AGE="$(command -v age || command -v rage || true)"
[ -n "$AGE" ] || die "neither age nor rage is on PATH"
for tool in curl jq sha256sum; do
  command -v "$tool" >/dev/null || die "$tool is not on PATH"
done

# Private scratch space for plaintext. umask first: the files must never be
# world-readable, not even for the instant between creation and chmod.
umask 077
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT INT TERM

# --- recipients -------------------------------------------------------------

# The admin public key, derived from the identity so the two cannot drift.
# age-keygen handles age identities; ssh-keygen handles SSH private keys.
admin_pub=""
if admin_pub="$(age-keygen -y "$IDENTITY" 2>/dev/null)"; then
  :
elif admin_pub="$(ssh-keygen -y -f "$IDENTITY" 2>/dev/null)"; then
  :
else
  die "cannot derive a public key from $IDENTITY (expected an age identity or an unencrypted SSH private key)"
fi

recipients_file="$WORK/recipients"
printf '%s\n' "$admin_pub" >"$recipients_file"

for f in ${EXTRA_RECIPIENT_FILES+"${EXTRA_RECIPIENT_FILES[@]}"}; do
  [ -r "$f" ] || die "extra recipients file $f is not readable"
  grep -v '^[[:space:]]*\(#.*\)\?$' "$f" >>"$recipients_file" || true
done

# Device host keys from the console. --fail turns an HTTP error into a
# non-zero exit instead of a body that would be parsed as an empty list and
# silently drop every device from the recipient set.
note "fetching device host keys from $SEXTANT_URL"
hostkeys_json="$WORK/hostkeys.json"
curl --fail --silent --show-error --location \
  --header "Authorization: Bearer $SEXTANT_API_TOKEN" \
  --header "Accept: application/json" \
  --output "$hostkeys_json" \
  "${SEXTANT_URL%/}/api/v1/hostkeys" \
  || die "could not read ${SEXTANT_URL%/}/api/v1/hostkeys (token? network?)"

device_count=0
while IFS= read -r key; do
  [ -n "$key" ] || continue
  # Shape check, mirroring the console's: a recipients file is fed to age and
  # committed, so nothing malformed gets in even if the API were compromised.
  case "$key" in
    "ssh-ed25519 "*|"ssh-rsa "*|"ecdsa-sha2-nistp256 "*|"ecdsa-sha2-nistp384 "*|"ecdsa-sha2-nistp521 "*) ;;
    *) die "console returned a host key that is not an SSH public key; aborting" ;;
  esac
  printf '%s\n' "$key" >>"$recipients_file"
  device_count=$((device_count + 1))
done < <(jq -r '.[].hostKey // empty' "$hostkeys_json")

# Canonicalise (sorted, unique) so a reordering is not mistaken for a change.
sort -u "$recipients_file" -o "$recipients_file"
recipients_hash="$(sha256sum <"$recipients_file" | cut -d' ' -f1)"
note "recipients: $(wc -l <"$recipients_file") unique, of which $device_count device host keys"
[ "$device_count" -gt 0 ] || note "WARNING: the console reported no device host keys - imaged devices may not be reporting them yet"

# --- select the files to rekey ----------------------------------------------

STATE_FILE="$SECRETS_DIR/.rekey-state"
shopt -s nullglob
age_files=("$SECRETS_DIR"/*.age)
shopt -u nullglob
[ "${#age_files[@]}" -gt 0 ] || die "no *.age files in $SECRETS_DIR"

todo=()
for f in "${age_files[@]}"; do
  base="$(basename "$f")"
  if [ "$FORCE" -eq 0 ] && [ -r "$STATE_FILE" ] &&
     grep -qxF "$recipients_hash  $base" "$STATE_FILE"; then
    continue
  fi
  todo+=("$f")
done

if [ "${#todo[@]}" -eq 0 ]; then
  note "all ${#age_files[@]} secrets are already encrypted for this recipient set; nothing to do"
  exit 0
fi
note "rekeying ${#todo[@]} of ${#age_files[@]} secrets"

if [ "$DRY_RUN" -eq 1 ]; then
  for f in "${todo[@]}"; do printf '  would rekey %s\n' "$(basename "$f")"; done
  exit 0
fi

# --- phase 1: decrypt everything before rewriting anything ------------------

for f in "${todo[@]}"; do
  base="$(basename "$f")"
  if ! "$AGE" --decrypt --identity "$IDENTITY" --output "$WORK/plain.$base" "$f" 2>"$WORK/err"; then
    # The age error names the file and the identity, never plaintext.
    printf 'rekey-secrets: cannot decrypt %s with %s\n' "$base" "$IDENTITY" >&2
    sed 's/^/  /' "$WORK/err" >&2
    die "aborting with nothing rewritten - the admin identity must be a recipient of every secret"
  fi
done

# --- phase 2: re-encrypt and replace in place -------------------------------

age_args=()
while IFS= read -r r; do age_args+=(--recipient "$r"); done <"$recipients_file"

for f in "${todo[@]}"; do
  base="$(basename "$f")"
  "$AGE" --encrypt "${age_args[@]}" --output "$WORK/new.$base" "$WORK/plain.$base"
  # Prove the round trip before overwriting: a secret nobody can open is worse
  # than one encrypted for too few recipients.
  "$AGE" --decrypt --identity "$IDENTITY" --output "$WORK/check.$base" "$WORK/new.$base" \
    || die "re-encrypted $base is not readable with the admin identity; $f left untouched"
  cmp -s "$WORK/plain.$base" "$WORK/check.$base" \
    || die "round trip changed the contents of $base; $f left untouched"
  mv "$WORK/new.$base" "$f"
  chmod 0644 "$f" # ciphertext: it is committed to the overlay repo
  printf '  rekeyed %s\n' "$base"
done

# --- record the recipient set so a rerun is a no-op -------------------------

# Every file is now at the current recipient set: the skipped ones were
# skipped precisely because they already carried this hash.
{
  printf '# rekey-secrets.sh state: <recipient-set sha256>  <file>. Public data.\n'
  for f in "${age_files[@]}"; do
    printf '%s  %s\n' "$recipients_hash" "$(basename "$f")"
  done
} >"$WORK/state"
mv "$WORK/state" "$STATE_FILE"
chmod 0644 "$STATE_FILE"

note "done. Review 'git diff --stat' in the overlay and commit."
