package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func key32() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestSealRoundTrip(t *testing.T) {
	s, err := New(key32())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !s.Enabled() {
		t.Fatal("sealer should be enabled with a key")
	}
	msg := []byte("smtp-app-password")
	sealed, err := s.Seal(msg)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, msg) {
		t.Fatal("sealed bytes leak the plaintext")
	}
	got, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestSealIsRandomized(t *testing.T) {
	s, _ := New(key32())
	a, _ := s.Seal([]byte("x"))
	b, _ := s.Seal([]byte("x"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same value must differ (random nonce)")
	}
}

func TestOpenRejectsTamper(t *testing.T) {
	s, _ := New(key32())
	sealed, _ := s.Seal([]byte("secret"))
	sealed[len(sealed)-1] ^= 0xff // flip a ciphertext bit
	if _, err := s.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext must not open")
	}
}

func TestDisabledSealer(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatalf("empty key should not error: %v", err)
	}
	if s.Enabled() {
		t.Fatal("empty key must yield a disabled sealer")
	}
	if _, err := s.Seal([]byte("x")); !errors.Is(err, ErrDisabled) {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}

func TestBadKeyErrors(t *testing.T) {
	if _, err := New("not-base64!!!"); err == nil {
		t.Fatal("malformed base64 key must error")
	}
	if _, err := New(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("wrong-length key must error")
	}
}
