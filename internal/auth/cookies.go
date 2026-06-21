package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	AdminCookieName = "preview_admin_session"
	CookieLifetime  = 30 * 24 * time.Hour
)

type Signer struct {
	secret []byte
}

func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < 32 {
		return nil, errors.New("cookie secret must be at least 32 bytes")
	}
	return &Signer{secret: secret}, nil
}

func LoadOrCreateSecret(dataDir, override string) ([]byte, error) {
	if override != "" {
		return []byte(override), nil
	}
	path := filepath.Join(dataDir, "secrets", "cookie-secret")
	if data, err := os.ReadFile(path); err == nil {
		decoded, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil {
			return nil, decodeErr
		}
		return decoded, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	encoded := base64.RawStdEncoding.EncodeToString(secret)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

func PreviewCookieName(slug string) string {
	return "preview_auth_" + slug
}

func (s *Signer) Token(subject string, expires time.Time) string {
	payload := subject + "|" + strconv.FormatInt(expires.UTC().Unix(), 10)
	payloadEncoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	sig := s.sign(payloadEncoded)
	return payloadEncoded + "." + sig
}

func (s *Signer) Verify(token, subject string, now time.Time) bool {
	payloadEncoded, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	expectedSig := s.sign(payloadEncoded)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) != 1 {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return false
	}
	rawSubject, rawExpires, ok := strings.Cut(string(payloadBytes), "|")
	if !ok || rawSubject != subject {
		return false
	}
	expiresUnix, err := strconv.ParseInt(rawExpires, 10, 64)
	if err != nil {
		return false
	}
	return now.Before(time.Unix(expiresUnix, 0))
}

func (s *Signer) SetCookie(w http.ResponseWriter, name, subject, path string, secure bool, now time.Time) {
	expires := now.Add(CookieLifetime)
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    s.Token(subject, expires),
		Path:     path,
		Expires:  expires,
		MaxAge:   int(CookieLifetime.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Signer) VerifyRequestCookie(r *http.Request, name, subject string, now time.Time) bool {
	cookie, err := r.Cookie(name)
	if err != nil {
		return false
	}
	return s.Verify(cookie.Value, subject, now)
}

func (s *Signer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = fmt.Fprint(mac, payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
