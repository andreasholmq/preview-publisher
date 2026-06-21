package slug

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
)

var reserved = map[string]struct{}{
	"admin":   {},
	"api":     {},
	"assets":  {},
	"healthz": {},
	"static":  {},
}

func Normalize(value string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	if len(s) < 3 || len(s) > 80 {
		return "", errors.New("slug must be 3 to 80 characters")
	}
	if _, ok := reserved[s]; ok {
		return "", errors.New("slug is reserved")
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return "", errors.New("slug cannot start or end with hyphen")
	}
	if strings.Contains(s, "--") {
		return "", errors.New("slug cannot contain repeated hyphens")
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return "", errors.New("slug may contain only lowercase letters, digits, and hyphens")
	}
	return s, nil
}

func Generate() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
	if len(encoded) > 16 {
		encoded = encoded[:16]
	}
	return encoded, nil
}
