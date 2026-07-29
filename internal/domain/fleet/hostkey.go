package fleet

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// hostkey.go: a device's SSH host public key as a first-class recorded fact.
//
// A freshly imaged device holds a host keypair, and agenix encrypts every
// secret for a fixed set of recipients. Until the fleet document knows the
// device's public key, nothing can be encrypted for it: on the device every
// secret fails to decrypt, the activation script exits non-zero, comin
// correctly refuses to switch, and the device stays frozen at its image-time
// generation forever. Recording the key is what breaks that deadlock - it is
// the input an operator's rekey run turns into age recipients.

// maxHostKeyLen caps a recorded key. An RSA-4096 key in authorized_keys form
// is ~750 bytes; 1024 leaves room for a comment without offering a reporter
// somewhere to park a payload.
const maxHostKeyLen = 1024

// hostKeyAlgos are the key types age accepts as an SSH recipient.
var hostKeyAlgos = []string{
	"ssh-ed25519",
	"ssh-rsa",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
}

// NormalizeHostKey validates one SSH public key in authorized_keys form
// ("<algo> <base64 blob> [comment]") and returns it with whitespace
// canonicalised. The check is structural, not cryptographic: the value ends
// up in an age recipients file and a nix expression, so it must be a single
// bounded line whose declared algorithm matches the blob it carries.
func NormalizeHostKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("host key is empty")
	}
	if len(key) > maxHostKeyLen {
		return "", fmt.Errorf("host key is %d bytes, over the %d-byte cap", len(key), maxHostKeyLen)
	}
	if i := strings.IndexFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }); i >= 0 {
		return "", fmt.Errorf(`host key holds a control character at byte %d (want one line: "<algo> <base64> [comment]")`, i)
	}
	fields := strings.Fields(key)
	if len(fields) < 2 {
		return "", fmt.Errorf(`host key is not in "<algo> <base64> [comment]" form`)
	}
	if !slices.Contains(hostKeyAlgos, fields[0]) {
		return "", fmt.Errorf("unsupported host key algorithm %q (want one of %s)", fields[0], strings.Join(hostKeyAlgos, ", "))
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", fmt.Errorf("host key body is not base64: %w", err)
	}
	// The blob opens with a length-prefixed string repeating the algorithm
	// name. A mismatch means the two halves do not belong together - an algo
	// prefix pasted onto another key's body - which would silently produce a
	// recipients file no device can use.
	if name, ok := sshBlobAlgo(blob); !ok || name != fields[0] {
		return "", fmt.Errorf("host key body does not carry algorithm %q", fields[0])
	}
	return strings.Join(fields, " "), nil
}

// sshBlobAlgo reads the leading length-prefixed string of an SSH wire-format
// public key blob, which by RFC 4253 is the key's algorithm name.
func sshBlobAlgo(blob []byte) (string, bool) {
	if len(blob) < 4 {
		return "", false
	}
	n := binary.BigEndian.Uint32(blob[:4])
	rest := blob[4:]
	// int64 on both sides: the widening never overflows, whatever int is.
	if n == 0 || int64(n) > int64(len(rest)) {
		return "", false
	}
	return string(rest[:n]), true
}

// HostKeyFingerprint is the short sha256 fingerprint of a recorded host key,
// for logs and the console: it identifies which key is on file without
// reprinting the key. It hashes the key material only, so an edited comment
// does not change the fingerprint; an unrecorded or malformed value yields "".
func HostKeyFingerprint(key string) string {
	fields := strings.Fields(key)
	if len(fields) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])[:12]
}

// SetDeviceHostKey records a device's SSH host public key on its asset
// record. Secrets are encrypted FOR this key, so a silent replacement would
// let one rogue report redirect which key a device's secrets are readable by:
// a device that already carries a DIFFERENT key is refused unless force is
// set. Only the imaging path passes force - re-imaging legitimately mints a
// new keypair. Re-reporting the key already on file is a no-op, so a station
// retrying its install report needs no special case.
func SetDeviceHostKey(tag, pubkey string, force bool) Mutation {
	return func(f *Fleet) error {
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		key, err := NormalizeHostKey(pubkey)
		if err != nil {
			return err
		}
		if cur := d.ITAM.HostKeyID; cur != "" && cur != key && !force {
			return fmt.Errorf("device %q already has host key sha256:%s on file; refusing to replace it with sha256:%s (re-image the device to rotate its key)",
				tag, HostKeyFingerprint(cur), HostKeyFingerprint(key))
		}
		d.ITAM.HostKeyID = key
		f.Devices[tag] = d
		return nil
	}
}
