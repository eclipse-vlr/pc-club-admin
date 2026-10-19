package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
	"unicode/utf8"
)

func Token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func validToken(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
func ValidID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func ValidPIN(v string) bool {
	if len(v) != 6 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func ValidPassword(v string) bool { return utf8.RuneCountInString(v) >= 12 && len(v) <= 256 }

// Secrets stores only Argon2id hashes in the DB. The PIN pepper stays outside it.
type Secrets struct{ Pepper string }

func (s Secrets) input(value string, pin bool) []byte {
	if !pin {
		return []byte(value)
	}
	mac := hmac.New(sha256.New, []byte(s.Pepper))
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
func (s Secrets) Hash(value string, pin bool) string {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		panic("secure random source unavailable")
	}
	hash := argon2.IDKey(s.input(value, pin), salt[:], 3, 64*1024, 2, 32)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s", enc.EncodeToString(salt[:]), enc.EncodeToString(hash))
}
func (s Secrets) Verify(encoded, value string, pin bool) bool {
	if len(value) > 256 {
		return false
	}
	parts := strings.Split(encoded, "$")
	// Accept exactly the parameters this release creates: no attacker-selected allocation.
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	got := argon2.IDKey(s.input(value, pin), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
