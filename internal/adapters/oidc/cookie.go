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
// The cookie name is additionally used as GCM additional authenticated data,
// so a ciphertext sealed by one secureCookie (e.g. sextant_flow) cannot be
// substituted for another (e.g. sextant_session) even though both currently
// share the same SessionKey. Note: this AAD binding means any cookie sealed
// before this change will fail to open afterwards - an acceptable one-time
// forced re-login, not a security regression.
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
	// Bind the ciphertext to its cookie name (AES-GCM additional data): the
	// session and flow cookies share one SessionKey, so without this a
	// validly-sealed flow cookie renamed to sextant_session would still
	// decrypt (into a sessionData carrying flow-state garbage) - the
	// authorize gate in Callback would never have run for it. Sealing and
	// opening under a mismatched name now fails GCM authentication.
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, []byte(c.name))), nil
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
	// Same additional data as seal: a ciphertext sealed under a different
	// cookie name fails authentication here rather than decrypting into the
	// wrong struct shape.
	plain, err := gcm.Open(nil, nonce, ct, []byte(c.name))
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
	// #nosec G124 - HttpOnly+Lax+sealed value are set; Secure is c.secure so a loopback dev HTTP host can still authenticate, on in production.
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
	// #nosec G124 - deletion cookie: empty value, MaxAge -1, HttpOnly set; it carries no data to protect.
	http.SetCookie(w, &http.Cookie{
		Name: c.name, Value: "", Path: "/", HttpOnly: true, Secure: c.secure, MaxAge: -1,
	})
}
