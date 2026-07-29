package fleet

import (
	"strings"
	"testing"
)

// Real keys (throwaway) so the blob's embedded algorithm name matches.
const (
	keyEd25519  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP root@dev-01"
	keyEd25519B = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIZeLH/5H0LFB6TECGsgpniOQbttXevMqd5OAAoFg0Yu root@dev-02"
	keyECDSA    = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBDHm0sUWojT63wsgcQQrLO/fj/Qc4ABnGxd2GfY5oOPZdjuADltmwVK0u0U94tDJAmu3A1tRl7L79zDGPyGFsaQ= root@dev-03"
	keyRSA      = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDoTDP62wyC7dU0sApVWTCRBiut9tfQAt3TtRmdOmqCkz/7b4z55xnZbFlxT9TkvTFV1QneV6gJhK5G4PqSZFX8ATyhQgULmUAZ9l/PU+kgJnkoaSvGHuQ10QADweS5Rx9QpvxpnRJLZZ+FsH2trf3s7xKiiccy5rxFbQqhymR1EZ/FOXURJJah3KY133Qmd+J2+Qzlte0KN9IiM3gjxUVRaBu7G2wTx4J6EccVIU+UhdU/rgE3/dJLeCoEHAjwEPX4BVp6zKfTSbpfqCWTxzwNYSnw7OOBbHEFpzUSDZ7fzg21h2eSVG73AMe6O5B9z2WipBMZW+LRuQgERyutNSsp root@dev-04"

	// ed25519 body under an algorithm label that does not match it.
	keyBodyAlgoMismatch = "ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP"
)

func TestNormalizeHostKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // "" means the input must be rejected
	}{
		{"ed25519 with comment", keyEd25519, keyEd25519},
		{"ecdsa nistp256", keyECDSA, keyECDSA},
		{"rsa", keyRSA, keyRSA},
		{"no comment", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP",
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP"},
		{"trailing newline trimmed", keyEd25519 + "\n", keyEd25519},
		{"surrounding whitespace trimmed", "  \t" + keyEd25519 + "  ", keyEd25519},
		{"repeated separator spaces collapsed", "ssh-ed25519   AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP   root@dev-01", keyEd25519},
		{"comment with spaces kept", keyEd25519 + " extra note", keyEd25519 + " extra note"},

		{"empty", "", ""},
		{"whitespace only", "   \n\t ", ""},
		{"algorithm only", "ssh-ed25519", ""},
		{"unknown algorithm", "ssh-dss AAAAB3NzaC1kc3MAAACBAKw=", ""},
		{"private key material", "-----BEGIN OPENSSH PRIVATE KEY-----", ""},
		{"embedded newline", keyEd25519 + "\n" + keyEd25519B, ""},
		{"embedded carriage return", "ssh-ed25519\rAAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP", ""},
		{"embedded tab", "ssh-ed25519\tAAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP", ""},
		{"embedded NUL", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP\x00 root@dev-01", ""},
		{"body not base64", "ssh-ed25519 not-base64!!", ""},
		{"body too short to carry an algorithm", "ssh-ed25519 AAAA", ""},
		{"body algorithm mismatch", keyBodyAlgoMismatch, ""},
		{"overlong", keyEd25519 + " " + strings.Repeat("x", maxHostKeyLen), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeHostKey(tt.in)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("NormalizeHostKey(%q) = %q, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeHostKey(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeHostKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHostKeyFingerprint(t *testing.T) {
	fp := HostKeyFingerprint(keyEd25519)
	if len(fp) != 12 {
		t.Fatalf("fingerprint %q has %d chars, want 12", fp, len(fp))
	}
	// The comment is not key material: editing it must not move the fingerprint.
	if other := HostKeyFingerprint(keyEd25519 + "-renamed"); other != fp {
		t.Errorf("comment changed the fingerprint: %q vs %q", other, fp)
	}
	if same := HostKeyFingerprint(keyEd25519B); same == fp {
		t.Errorf("two different keys share fingerprint %q", fp)
	}
	for _, bad := range []string{"", "ssh-ed25519", "ssh-ed25519 not-base64!!"} {
		if got := HostKeyFingerprint(bad); got != "" {
			t.Errorf("HostKeyFingerprint(%q) = %q, want empty", bad, got)
		}
	}
}

func TestSetDeviceHostKey(t *testing.T) {
	newFleet := func(recorded string) *Fleet {
		d := Device{Hardware: "hp-g4"}
		d.ITAM.HostKeyID = recorded
		return &Fleet{Devices: map[string]Device{"lt-1": d}}
	}

	tests := []struct {
		name     string
		recorded string
		tag      string
		key      string
		force    bool
		wantErr  string // substring; "" means the write must succeed
		wantKey  string
	}{
		{name: "records a first key", tag: "lt-1", key: keyEd25519, wantKey: keyEd25519},
		{name: "canonicalises what it records", tag: "lt-1", key: "  " + keyEd25519 + "\n", wantKey: keyEd25519},
		{name: "re-reporting the same key is a no-op", recorded: keyEd25519, tag: "lt-1", key: keyEd25519, wantKey: keyEd25519},
		{name: "same key, different comment, is still a replacement",
			recorded: keyEd25519, tag: "lt-1", key: keyEd25519 + "-renamed", wantErr: "already has host key", wantKey: keyEd25519},
		{name: "replacement without force is refused",
			recorded: keyEd25519, tag: "lt-1", key: keyEd25519B, wantErr: "already has host key", wantKey: keyEd25519},
		{name: "imaging replaces with force",
			recorded: keyEd25519, tag: "lt-1", key: keyEd25519B, force: true, wantKey: keyEd25519B},
		{name: "unknown device", tag: "ghost", key: keyEd25519, wantErr: `unknown device "ghost"`},
		{name: "malformed key is refused even with force",
			recorded: keyEd25519, tag: "lt-1", key: "ssh-ed25519 not-base64!!", force: true, wantErr: "not base64", wantKey: keyEd25519},
		{name: "empty key is refused", tag: "lt-1", key: "", wantErr: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFleet(tt.recorded)
			err := SetDeviceHostKey(tt.tag, tt.key, tt.force)(f)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("SetDeviceHostKey: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("SetDeviceHostKey = nil, want error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("SetDeviceHostKey = %v, want error containing %q", err, tt.wantErr)
			}
			if got := f.Devices["lt-1"].ITAM.HostKeyID; got != tt.wantKey {
				t.Errorf("recorded key = %q, want %q", got, tt.wantKey)
			}
		})
	}
}

// A rejected replacement must never leak the key material into the error -
// the fingerprint is what an operator needs to tell two keys apart.
func TestSetDeviceHostKeyErrorNamesFingerprintsOnly(t *testing.T) {
	f := &Fleet{Devices: map[string]Device{"lt-1": {ITAM: ITAM{HostKeyID: keyEd25519}}}}
	err := SetDeviceHostKey("lt-1", keyEd25519B, false)(f)
	if err == nil {
		t.Fatal("replacement without force was accepted")
	}
	for _, fp := range []string{HostKeyFingerprint(keyEd25519), HostKeyFingerprint(keyEd25519B)} {
		if !strings.Contains(err.Error(), fp) {
			t.Errorf("error %q does not name fingerprint %s", err, fp)
		}
	}
}
