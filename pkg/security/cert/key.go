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
func ParsePrivateKey(pkey []byte) (*ecdsa.PrivateKey, error) {
	b, _ := pem.Decode(pkey)
	if b == nil {
		return nil, fmt.Errorf("no PEM data found")
	}

	u, err := x509.ParseECPrivateKey(b.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse EC private key: %w", err)
	}

	return u, nil
}
