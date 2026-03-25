package cert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// PrivateKey holds an ECDSA private key.
type PrivateKey struct {
	*ecdsa.PrivateKey
}

// GetPrivateKey generates a new ECDSA P-256 private key.
func GetPrivateKey() (*PrivateKey, error) {
	pkey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ECDSA key: %w", err)
	}

	return &PrivateKey{pkey}, nil
}

// String returns the PEM-encoded private key.
func (p *PrivateKey) String() string {
	b, err := x509.MarshalECPrivateKey(p.PrivateKey)
	if err != nil {
		return ""
	}

	var w bytes.Buffer
	if err := pem.Encode(&w, &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: b,
	}); err != nil {
		return ""
	}

	return w.String()
}

// ParsePrivateKey parses a PEM-encoded ECDSA private key.
// Supports plain EC PRIVATE KEY, KEK-encrypted, and PIN-encrypted formats.
// For KEK-encrypted keys, provide the KEK via ParsePrivateKeyWithKEK instead.
func ParsePrivateKey(pkey []byte) (*ecdsa.PrivateKey, error) {
	b, _ := pem.Decode(pkey)
	if b == nil {
		return nil, fmt.Errorf("no PEM data found")
	}

	// Reject encrypted keys — caller must use DecryptKeyWithPIN or DecryptKeyWithKEK
	if b.Type == "ENCRYPTED PRIVATE KEY" || b.Type == "KEK ENCRYPTED PRIVATE KEY" {
		return nil, fmt.Errorf("key is encrypted, use appropriate decryption function")
	}

	u, err := x509.ParseECPrivateKey(b.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse EC private key: %w", err)
	}

	return u, nil
}

// ParsePrivateKeyAuto parses a private key PEM, auto-detecting the encryption type.
// For KEK-encrypted keys, provide the KEK. For plaintext keys, kek can be nil.
// PIN-encrypted keys cannot be parsed with this function (use DecryptKeyWithPIN).
func ParsePrivateKeyAuto(pkey []byte, kek []byte) (*ecdsa.PrivateKey, error) {
	b, _ := pem.Decode(pkey)
	if b == nil {
		return nil, fmt.Errorf("no PEM data found")
	}

	switch b.Type {
	case "KEK ENCRYPTED PRIVATE KEY":
		return DecryptKeyWithKEK(pkey, kek)
	case "ENCRYPTED PRIVATE KEY":
		return nil, fmt.Errorf("PIN-encrypted key requires user PIN")
	default:
		return x509.ParseECPrivateKey(b.Bytes)
	}
}
