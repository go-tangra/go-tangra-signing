// Package pincrypto protects a signer's private key with the signer's PIN
// (research D5, SR-003): the PKCS#8 key is encrypted with AES-256-GCM under a
// key derived from the PIN with PBKDF2-HMAC-SHA256 (standard library), bound
// to associated data naming the certificate so a key blob cannot be moved to
// another row. The envelope is versioned so the parameters can be raised
// later; the PIN itself is never stored. The package also holds the PIN rules
// and the lockout policy (FR-031, FR-034).
//
// Envelope v1: version(1) | iterations(4, big endian) | salt(32) | nonce(12) | ciphertext+tag.
package pincrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"time"
	"unicode"
	"unicode/utf8"
)

// Errors.
var (
	ErrPIN    = errors.New("pincrypto: wrong PIN")
	ErrFormat = errors.New("pincrypto: malformed key envelope")
	ErrRule   = errors.New("pincrypto: PIN does not meet the rules")
)

const (
	version1  = 1
	saltLen   = 32
	nonceLen  = 12
	headerLen = 1 + 4 + saltLen + nonceLen
	// MinIterations and MaxIterations bound the key derivation of an opened
	// envelope (a tampered header must not force a huge derivation).
	MinIterations = 100_000
	MaxIterations = 10_000_000
)

var randReader io.Reader = rand.Reader

// derive is the PBKDF2 derivation (a variable so tests can simulate the
// FIPS-mode refusal the standard library may return).
var derive = func(pin string, salt []byte, iter int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pin, salt, iter, 32)
}

func gcm(key []byte) cipher.AEAD {
	block, _ := aes.NewCipher(key) // a 32-byte key never fails
	g, _ := cipher.NewGCM(block)
	return g
}

// Seal encrypts key (PKCS#8 DER) under pin with iter PBKDF2 iterations.
func Seal(key []byte, pin string, ad []byte, iter int) ([]byte, error) {
	if iter < MinIterations || iter > MaxIterations {
		return nil, ErrFormat
	}
	out := make([]byte, headerLen, headerLen+len(key)+16)
	out[0] = version1
	binary.BigEndian.PutUint32(out[1:5], uint32(iter)) // #nosec G115 -- bounded above
	if _, err := io.ReadFull(randReader, out[5:5+saltLen]); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(randReader, out[5+saltLen:headerLen]); err != nil {
		return nil, err
	}
	k, err := derive(pin, out[5:5+saltLen], iter)
	if err != nil {
		return nil, err
	}
	return gcm(k).Seal(out, out[5+saltLen:headerLen], key, ad), nil
}

// Open decrypts an envelope with pin; a wrong PIN (or tampering) is ErrPIN.
func Open(blob []byte, pin string, ad []byte) ([]byte, error) {
	if len(blob) < headerLen+16 || blob[0] != version1 {
		return nil, ErrFormat
	}
	iter := int(binary.BigEndian.Uint32(blob[1:5]))
	if iter < MinIterations || iter > MaxIterations {
		return nil, ErrFormat
	}
	k, err := derive(pin, blob[5:5+saltLen], iter)
	if err != nil {
		return nil, err
	}
	pt, err := gcm(k).Open(nil, blob[5+saltLen:headerLen], blob[headerLen:], ad)
	if err != nil {
		return nil, ErrPIN
	}
	return pt, nil
}

// Rules are the PIN rules (FR-031: 6–32 characters by default).
type Rules struct {
	Min, Max int
}

// Check reports whether pin meets the rules: min..max characters, valid
// UTF-8, no control characters.
func (r Rules) Check(pin string) error {
	if !utf8.ValidString(pin) {
		return ErrRule
	}
	n := utf8.RuneCountInString(pin)
	if n < r.Min || n > r.Max {
		return ErrRule
	}
	for _, c := range pin {
		if unicode.IsControl(c) {
			return ErrRule
		}
	}
	return nil
}

// Lockout is the wrong-PIN policy (FR-034: 5 failures lock for 15 minutes).
type Lockout struct {
	Attempts int
	Duration time.Duration
}

// Locked reports whether a lock is in force at now.
func Locked(lockedUntil *time.Time, now time.Time) bool {
	return lockedUntil != nil && now.Before(*lockedUntil)
}

// Until is the end of a lock starting at now.
func (l Lockout) Until(now time.Time) time.Time { return now.Add(l.Duration) }

// AttemptsLeft is how many wrong PINs remain after failures.
func (l Lockout) AttemptsLeft(failures int) int {
	if left := l.Attempts - failures; left > 0 {
		return left
	}
	return 0
}
