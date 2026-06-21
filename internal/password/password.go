package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memory      = 64 * 1024
	iterations  = 3
	parallelism = 1
	saltLen     = 16
	keyLen      = 32
)

func Generate() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password cannot be empty")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func Verify(password, encoded string) bool {
	params, salt, expected, err := parse(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

type parameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parse(encoded string) (parameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return parameters{}, nil, nil, errors.New("invalid password hash")
	}
	params, err := parseParams(parts[3])
	if err != nil {
		return parameters{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return parameters{}, nil, nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return parameters{}, nil, nil, err
	}
	return params, salt, key, nil
}

func parseParams(raw string) (parameters, error) {
	var p parameters
	for _, item := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return p, errors.New("invalid argon2 parameters")
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return p, err
		}
		switch key {
		case "m":
			p.memory = uint32(parsed)
		case "t":
			p.iterations = uint32(parsed)
		case "p":
			if parsed > 255 {
				return p, errors.New("invalid argon2 parallelism")
			}
			p.parallelism = uint8(parsed)
		default:
			return p, errors.New("unknown argon2 parameter")
		}
	}
	if p.memory == 0 || p.iterations == 0 || p.parallelism == 0 {
		return p, errors.New("missing argon2 parameter")
	}
	return p, nil
}
