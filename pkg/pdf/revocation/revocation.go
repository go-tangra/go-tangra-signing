// Package revocation handles OCSP and CRL revocation data embedding for PAdES signatures.
package revocation

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/crypto/ocsp"
)

// InfoArchival holds revocation data to be embedded in a PKCS7 signature.
// This implements the Adobe RevocationInfoArchival attribute (OID 1.2.840.113583.1.1.8).
type InfoArchival struct {
	CRL   CRL   `asn1:"tag:0,optional,explicit"`
	OCSP  OCSP  `asn1:"tag:1,optional,explicit"`
	Other Other `asn1:"tag:2,optional,explicit"`
}

type CRL []asn1.RawValue
type OCSP []asn1.RawValue
type Other []asn1.RawValue

// AddOCSP adds an OCSP response to the revocation data.
func (i *InfoArchival) AddOCSP(ocspResponse []byte) error {
	i.OCSP = append(i.OCSP, asn1.RawValue{FullBytes: ocspResponse})
	return nil
}

// AddCRL adds a CRL to the revocation data.
func (i *InfoArchival) AddCRL(crlBytes []byte) error {
	i.CRL = append(i.CRL, asn1.RawValue{FullBytes: crlBytes})
	return nil
}

// EmbedOCSPRevocationStatus fetches and embeds an OCSP response for the certificate.
func EmbedOCSPRevocationStatus(cert, issuer *x509.Certificate, i *InfoArchival) error {
	if len(cert.OCSPServer) == 0 {
		return fmt.Errorf("no OCSP server URL in certificate")
	}

	req, err := ocsp.CreateRequest(cert, issuer, nil)
	if err != nil {
		return fmt.Errorf("failed to create OCSP request: %w", err)
	}

	ocspURL := cert.OCSPServer[0] + "/" + base64.StdEncoding.EncodeToString(req)
	resp, err := http.Get(ocspURL)
	if err != nil {
		return fmt.Errorf("failed to fetch OCSP response: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read OCSP response: %w", err)
	}

	// Validate the response
	_, err = ocsp.ParseResponseForCert(body, cert, issuer)
	if err != nil {
		return fmt.Errorf("failed to validate OCSP response: %w", err)
	}

	return i.AddOCSP(body)
}

// EmbedCRLRevocationStatus fetches and embeds a CRL for the certificate.
func EmbedCRLRevocationStatus(cert *x509.Certificate, i *InfoArchival) error {
	if len(cert.CRLDistributionPoints) == 0 {
		return fmt.Errorf("no CRL distribution points in certificate")
	}

	resp, err := http.Get(cert.CRLDistributionPoints[0])
	if err != nil {
		return fmt.Errorf("failed to fetch CRL: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read CRL: %w", err)
	}

	return i.AddCRL(body)
}
