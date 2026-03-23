package cert

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"
)

// CertRevocationList holds a DER-encoded CRL.
type CertRevocationList struct {
	Byte []byte
}

// CreateCRL creates a new Certificate Revocation List.
func CreateCRL(
	pkey *ecdsa.PrivateKey,
	caCert *x509.Certificate,
	existingCRL *x509.RevocationList,
	nextUpdate time.Time,
) (*CertRevocationList, *big.Int, error) {
	now := time.Now()
	number := big.NewInt(now.Unix())

	template := &x509.RevocationList{
		Number:     number,
		ThisUpdate: now,
		NextUpdate: nextUpdate,
	}

	// Carry over existing revoked certificates
	if existingCRL != nil {
		template.RevokedCertificateEntries = existingCRL.RevokedCertificateEntries
	}

	crlBytes, err := x509.CreateRevocationList(rand.Reader, template, caCert, pkey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CRL: %w", err)
	}

	return &CertRevocationList{Byte: crlBytes}, number, nil
}

// RevokeCertificate adds a certificate to the CRL and returns the updated CRL.
func RevokeCertificate(
	crlBytes []byte,
	cert *x509.Certificate,
	caCert *x509.Certificate,
	pkey *ecdsa.PrivateKey,
	nextUpdate time.Time,
) (*CertRevocationList, *big.Int, error) {
	// Parse existing CRL
	var existingEntries []x509.RevocationListEntry
	if len(crlBytes) > 0 {
		existingCRL, err := x509.ParseRevocationList(crlBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse existing CRL: %w", err)
		}
		existingEntries = existingCRL.RevokedCertificateEntries
	}

	// Add new revocation entry
	now := time.Now()
	newEntry := x509.RevocationListEntry{
		SerialNumber:   cert.SerialNumber,
		RevocationTime: now,
		ReasonCode:     0, // Unspecified
		Extensions: []pkix.Extension{
			{
				Id:    []int{2, 5, 29, 21}, // CRL reason code OID
				Value: []byte{0x0a, 0x01, 0x00},
			},
		},
	}
	existingEntries = append(existingEntries, newEntry)

	number := big.NewInt(now.Unix())
	template := &x509.RevocationList{
		Number:                      number,
		ThisUpdate:                  now,
		NextUpdate:                  nextUpdate,
		RevokedCertificateEntries:   existingEntries,
	}

	crlResult, err := x509.CreateRevocationList(rand.Reader, template, caCert, pkey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create updated CRL: %w", err)
	}

	return &CertRevocationList{Byte: crlResult}, number, nil
}
