package oidc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
)

// secureCookie is an authenticated-encrypted (AES-256-GCM) cookie, used for
// the session and the short-lived login-flow state. Tamper-proof and
// confidential; the key never leaves the server. Ported from the proven PoC.
type secureCookie struct {
	name   string
	key    []byte // 32 bytes (AES-256)
	secure bool
	maxAge int
}

func newSecureCookie(name string, key []byte, secure bool, maxAge int) (*secureCookie, error) {
	if len(key) != 32 {
		return nil, errors.New("session key must be exactly 32 bytes")
	}
	return &secureCookie{name: name, key: key, secure: secure, maxAge: maxAge}, nil
}

func (c *secureCookie) seal(v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, nil)), nil
}

func (c *secureCookie) open(s string, out any) error {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if len(data) < gcm.NonceSize() {
		return errors.New("cookie too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, out)
}

func (c *secureCookie) set(w http.ResponseWriter, v any) error {
	val, err := c.seal(v)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     c.name,
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.secure,
		// Lax survives the IdP redirect back; the CSRF token guards POSTs.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   c.maxAge,
	})
	return nil
}

func (c *secureCookie) get(r *http.Request, out any) error {
	ck, err := r.Cookie(c.name)
	if err != nil {
		return err
	}
	return c.open(ck.Value, out)
}

func (c *secureCookie) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.name, Value: "", Path: "/", HttpOnly: true, Secure: c.secure, MaxAge: -1,
	})
}
