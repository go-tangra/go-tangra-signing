package cert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"
)

var ErrDecodeCACert = errors.New("can't decode CA cert file")

// Certificate holds certificate generation parameters.
type Certificate struct {
	SerialNumber     *big.Int
	Subject          pkix.Name
	NotBefore        time.Time
	NotAfter         time.Time
	IPAddress        []net.IP
	DNSNames         []string
	IsCA             bool
	Parent           *x509.Certificate
	ParentPrivateKey any
	KeyUsage         x509.KeyUsage
	ExtentedKeyUsage []x509.ExtKeyUsage
	SubjectKeyId     []byte
	AuthorityKeyId   []byte
}

// Result holds a generated certificate in DER and parsed formats.
type Result struct {
	ByteCert []byte
	Cert     *x509.Certificate
}

// GetSerial generates a random 128-bit serial number.
func GetSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial: %w", err)
	}
	return serial, nil
}

// SetTemplate creates an x509.Certificate template from the configuration.
func (c *Certificate) SetTemplate() x509.Certificate {
	return x509.Certificate{
		SerialNumber:          c.SerialNumber,
		Subject:               c.Subject,
		NotBefore:             c.NotBefore,
		NotAfter:              c.NotAfter,
		ExtKeyUsage:           c.ExtentedKeyUsage,
		KeyUsage:              c.KeyUsage,
		IsCA:                  c.IsCA,
		IPAddresses:           c.IPAddress,
		DNSNames:              c.DNSNames,
		BasicConstraintsValid: true,
		SubjectKeyId:          c.SubjectKeyId,
		AuthorityKeyId:        c.AuthorityKeyId,
	}
}

// GetCertificate generates an X.509 certificate signed by the parent (or self-signed).
func (c *Certificate) GetCertificate(pkey *ecdsa.PrivateKey) (*Result, error) {
	serial, err := GetSerial()
	if err != nil {
		return nil, err
	}

	c.SerialNumber = serial
	template := c.SetTemplate()

	if c.Parent == nil {
		c.Parent = &template
	}

	if c.ParentPrivateKey == nil {
		c.ParentPrivateKey = pkey
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, c.Parent, &pkey.PublicKey, c.ParentPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	return &Result{ByteCert: der, Cert: c.Parent}, nil
}

// String returns the PEM-encoded certificate.
func (r *Result) String() string {
	var w bytes.Buffer

	if err := pem.Encode(&w, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: r.ByteCert,
	}); err != nil {
		return ""
	}

	return w.String()
}

// ParseCertificate parses a PEM-encoded X.509 certificate.
func ParseCertificate(cert []byte) (*x509.Certificate, error) {
	p, _ := pem.Decode(cert)
	if p == nil {
		return nil, ErrDecodeCACert
	}

	c, err := x509.ParseCertificate(p.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return c, nil
}
