// Package sealed keeps the module-held private keys (tenant signing CAs, the
// audit-trail system certificates and administrator certificates) encrypted
// at rest with the module key-encryption key: an envelope (per-value
// AES-256-GCM data key wrapped by the KEK, the scheme of the other v4 modules)
// bound to associated data naming the certificate, so a sealed key cannot be
// moved to another row (research D4, SR-003). Signer keys are PIN-protected
// instead (package pincrypto).
package sealed

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// MaxPlaintextBytes bounds a sealed value (a PKCS#8 key is far below it).
const MaxPlaintextBytes = 8 << 10

// Errors.
var (
	ErrKEK      = errors.New("sealed: KEK must be 32 bytes")
	ErrTampered = errors.New("sealed: ciphertext rejected")
	ErrTooLarge = errors.New("sealed: value exceeds 8 KiB")
)

var randReader io.Reader = rand.Reader

// Envelope seals and opens values with the module KEK.
type Envelope struct {
	kek cipher.AEAD
	kcv []byte
}

// NewEnvelope requires a 32-byte KEK.
func NewEnvelope(kek []byte) (*Envelope, error) {
	if len(kek) != 32 {
		return nil, ErrKEK
	}
	m := hmac.New(sha256.New, kek)
	m.Write([]byte("go-tangra-signing key check v1"))
	return &Envelope{kek: gcm(kek), kcv: m.Sum(nil)[:16]}, nil
}

// KeyCheck identifies the KEK without revealing it: backups carry it so an
// import keeps sealed keys only when the target holds the same KEK
// (research D15).
func (e *Envelope) KeyCheck() []byte { return append([]byte(nil), e.kcv...) }

// gcm builds AES-256-GCM over a 32-byte key; neither constructor can fail
// for a key of that length.
func gcm(key []byte) cipher.AEAD {
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	return g
}

// LoadKEK reads the key from a file (base64 or raw 32 bytes, mode 0600
// recommended) or an environment variable (base64).
func LoadKEK(source, path, env string) ([]byte, error) {
	var raw []byte
	switch source {
	case "file":
		b, err := os.ReadFile(path) // #nosec G304 -- operator-supplied key path
		if err != nil {
			return nil, fmt.Errorf("sealed: kek: %w", err)
		}
		raw = b
	case "env":
		v := os.Getenv(env)
		if v == "" {
			return nil, fmt.Errorf("sealed: kek: environment variable %q is empty", env)
		}
		raw = []byte(v)
	default:
		return nil, fmt.Errorf("sealed: kek: unknown source %q", source)
	}
	return decodeKEK(raw)
}

func decodeKEK(raw []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(raw) == 32 {
		return raw, nil
	}
	return nil, ErrKEK
}

// MaxDataBytes bounds SealData (field values of one signer).
const MaxDataBytes = 4 << 20

// Seal encrypts plaintext bound to associated data (see ADCertKey).
// Layout: nonce | wrapped DEK | nonce | ciphertext.
func (e *Envelope) Seal(plaintext, ad []byte) ([]byte, error) {
	return e.seal(plaintext, ad, MaxPlaintextBytes)
}

// SealData is Seal for larger records (up to MaxDataBytes); Open opens both.
func (e *Envelope) SealData(plaintext, ad []byte) ([]byte, error) {
	return e.seal(plaintext, ad, MaxDataBytes)
}

func (e *Envelope) seal(plaintext, ad []byte, max int) ([]byte, error) {
	if len(plaintext) > max {
		return nil, ErrTooLarge
	}
	dek := make([]byte, 32)
	n1 := make([]byte, e.kek.NonceSize())
	n2 := make([]byte, e.kek.NonceSize())
	if _, err := io.ReadFull(randReader, dek); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(randReader, n1); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(randReader, n2); err != nil {
		return nil, err
	}
	g := gcm(dek)
	out := append([]byte{}, n1...)
	out = append(out, e.kek.Seal(nil, n1, dek, ad)...)
	out = append(out, n2...)
	return append(out, g.Seal(nil, n2, plaintext, ad)...), nil
}

// Open decrypts a value produced by Seal with the same associated data.
func (e *Envelope) Open(blob, ad []byte) ([]byte, error) {
	ns := e.kek.NonceSize()
	wrapLen := 32 + e.kek.Overhead()
	if len(blob) < ns+wrapLen+ns+16 {
		return nil, ErrTampered
	}
	dek, err := e.kek.Open(nil, blob[:ns], blob[ns:ns+wrapLen], ad)
	if err != nil {
		return nil, ErrTampered
	}
	rest := blob[ns+wrapLen:]
	pt, err := gcm(dek).Open(nil, rest[:ns], rest[ns:], ad)
	if err != nil {
		return nil, ErrTampered
	}
	return pt, nil
}

// ADCertKey is the associated data of a sealed certificate private key.
func ADCertKey(certificateID string) []byte { return []byte("certkey:" + certificateID) }
