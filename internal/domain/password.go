package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for password identities: OWASP's 12 MiB, 3 passes, 1
// thread setting with the memory raised until one check takes about 20 ms on
// a 2025 laptop core. Sign-in bounds how many checks run at once, so memory
// per check is also what a flood of sign-ins can make the server hold. The
// encoded form follows the PHC string format so the parameters can change
// without a migration.
const (
	argonTime    = 3
	argonMemory  = 13 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
	MinPassword  = 8
)

// PasswordKey is the identity key a password login is stored and found
// under: logins match without regard to case or surrounding space.
func PasswordKey(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// HashPassword derives an argon2id hash for storage in identities.secret.
func HashPassword(password string) ([]byte, error) {
	if len(password) < MinPassword {
		return nil, invalid("password", fmt.Sprintf("at least %d characters", MinPassword))
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return []byte(fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		enc.EncodeToString(salt), enc.EncodeToString(key))), nil
}

// VerifyPassword reports whether password matches a hash from HashPassword.
func VerifyPassword(hash []byte, password string) (bool, error) {
	parts := strings.Split(string(hash), "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unrecognised password hash")
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse hash version: %w", err)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, fmt.Errorf("parse hash parameters: %w", err)
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode key: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
