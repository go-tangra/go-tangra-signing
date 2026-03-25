package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"time"
	"unicode"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pdfsign/sign"
	"github.com/menta2k/go-transliteration"

	"github.com/go-tangra/go-tangra-signing/internal/data"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	securitycert "github.com/go-tangra/go-tangra-signing/pkg/security/cert"
)

const (
	defaultCertValidityYears = 2
	defaultCertOrg           = "GoTangra Signing"
	tenantCAValidityYears    = 10
)

// transliterateCN converts a name to Latin characters suitable for X.509 CN.
// Cyrillic and Greek names are transliterated; other scripts are passed through.
func transliterateCN(name string) string {
	if name == "" {
		return name
	}

	// Check if name contains non-Latin characters
	hasNonLatin := false
	for _, r := range name {
		if r > 127 && unicode.IsLetter(r) {
			hasNonLatin = true
			break
		}
	}
	if !hasNonLatin {
		return name
	}

	// Try transliteration with Bulgarian (bg) as default — covers most Cyrillic
	// The library auto-detects the script and transliterates accordingly.
	result := transliteration.ToLatin(name, "bg", false)
	if result != "" && result != name {
		return result
	}

	return name
}

// ensureTenantCA creates a tenant CA certificate if none exists.
func ensureTenantCA(ctx context.Context, certRepo *data.CertificateRepo, tenantID uint32) (*ent.Certificate, error) {
	existing, err := certRepo.GetTenantCA(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	// Generate CA key
	caKey, err := securitycert.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}

	caCertConfig := &securitycert.Certificate{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("Tenant %d Signing CA", tenantID),
			Organization: []string{defaultCertOrg},
		},
		NotBefore:        time.Now(),
		NotAfter:         time.Now().AddDate(tenantCAValidityYears, 0, 0),
		IsCA:             true,
		KeyUsage:         x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtentedKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}

	caResult, err := caCertConfig.GetCertificate(caKey.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("generate CA cert: %w", err)
	}

	entity, err := certRepo.Create(ctx, tenantID,
		caCertConfig.Subject.CommonName, defaultCertOrg,
		caResult, caKey, true, "", nil,
	)
	if err != nil {
		return nil, fmt.Errorf("store CA cert: %w", err)
	}

	return entity, nil
}

// generateUserCertificate creates a user certificate, optionally signed by a tenant CA.
// The private key is encrypted with the provided PIN.
func generateUserCertificate(
	ctx context.Context,
	certRepo *data.CertificateRepo,
	tenantID uint32,
	signerName, signerEmail, pin string,
	pendingCertID string,
) (*ent.Certificate, error) {
	_ = signerEmail // reserved for future email embedding in cert
	// Transliterate name for CN (Cyrillic → Latin)
	cn := transliterateCN(signerName)

	// Generate user key
	userKey, err := securitycert.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate user key: %w", err)
	}

	certConfig := &securitycert.Certificate{
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{defaultCertOrg},
		},
		NotBefore:        time.Now(),
		NotAfter:         time.Now().AddDate(defaultCertValidityYears, 0, 0),
		IsCA:             false,
		KeyUsage:         x509.KeyUsageDigitalSignature,
		ExtentedKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}

	// Try to use tenant CA as issuer
	var parentID string
	tenantCA, err := ensureTenantCA(ctx, certRepo, tenantID)
	if err == nil && tenantCA != nil {
		parentX509, parseErr := securitycert.ParseCertificate([]byte(tenantCA.CertPem))
		if parseErr == nil {
			parentKey, keyErr := securitycert.ParsePrivateKeyAuto([]byte(tenantCA.KeyPemEncrypted), certRepo.KEK())
			if keyErr == nil {
				certConfig.Parent = parentX509
				certConfig.ParentPrivateKey = parentKey
				parentID = tenantCA.ID
			}
		}
	}

	result, err := certConfig.GetCertificate(userKey.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("generate user cert: %w", err)
	}

	// Encrypt private key with PIN
	encryptedKeyPEM, err := securitycert.EncryptKeyWithPIN(userKey.PrivateKey, pin)
	if err != nil {
		return nil, fmt.Errorf("encrypt key with PIN: %w", err)
	}

	// Complete the pending certificate record
	entity, err := certRepo.CompleteSetup(ctx, pendingCertID, result, encryptedKeyPEM, false, parentID)
	if err != nil {
		return nil, fmt.Errorf("complete cert setup: %w", err)
	}

	return entity, nil
}

// applyLocalPAdESSignature signs a PDF with a user's local certificate.
// Returns the storage key of the signed PDF.
func applyLocalPAdESSignature(
	ctx context.Context,
	certEntity *ent.Certificate,
	privateKey *ecdsa.PrivateKey,
	certRepo *data.CertificateRepo,
	storage *data.StorageClient,
	pdfContent []byte,
	tenantID uint32,
	submissionID string,
	signerName string,
	fieldsSnapshot []map[string]interface{},
	signerOrder int,
) ([]byte, error) {
	// Parse X.509 certificate
	x509Cert, err := securitycert.ParseCertificate([]byte(certEntity.CertPem))
	if err != nil {
		return nil, fmt.Errorf("parse cert: %w", err)
	}

	fixedDate := time.Now().UTC().Truncate(time.Second)

	// Find signature field position from template snapshot
	sigAppearance := sign.Appearance{Visible: false}
	for _, f := range fieldsSnapshot {
		fType := getStringField(f, "type")
		fIdx := getIntField(f, "submitter_index")
		if (fType == "signature" || fType == "initials") && fIdx == signerOrder {
			pageW, pageH := detectPageSize(pdfContent)
			xPct := getFloat64Field(f, "x_percent")
			yPct := getFloat64Field(f, "y_percent")
			hPct := getFloat64Field(f, "height_percent")
			pgNum := getIntField(f, "page_number")
			if pgNum <= 0 {
				pgNum = 1
			}

			x := xPct / 100.0 * pageW
			h := hPct / 100.0 * pageH
			yTop := yPct / 100.0 * pageH

			imgH := int(h) * 3
			if imgH < 80 {
				imgH = 80
			}
			imgW := imgH * 3

			issuerCN := x509Cert.Issuer.CommonName
			stampImg := generateSignatureStampImage(signerName, issuerCN, fixedDate, imgW, imgH)

			stampW := h * float64(imgW) / float64(imgH)
			yBottom := pageH - yTop - h

			sigAppearance = sign.Appearance{
				Visible:     true,
				Page:        uint32(pgNum),
				LowerLeftX:  x,
				LowerLeftY:  yBottom,
				UpperRightX: x + stampW,
				UpperRightY: yBottom + h,
				Image:       stampImg,
			}
			break
		}
	}

	signData := sign.SignData{
		Certificate: x509Cert,
		Signer:      privateKey,
		Signature: sign.SignDataSignature{
			CertType:   sign.ApprovalSignature,
			DocMDPPerm: sign.AllowFillingExistingFormFieldsAndSignaturesPerms,
			Info: sign.SignDataSignatureInfo{
				Name:     signerName,
				Location: "GoTangra Signing",
				Reason:   "Document signing",
				Date:     fixedDate,
			},
		},
		DigestAlgorithm: crypto.SHA256,
		Appearance:      sigAppearance,
	}

	// Build certificate chain if parent exists
	if certEntity.ParentID != nil {
		issuerCert, issErr := certRepo.GetByID(ctx, *certEntity.ParentID)
		if issErr == nil && issuerCert != nil {
			issuerX509, parseErr := securitycert.ParseCertificate([]byte(issuerCert.CertPem))
			if parseErr == nil {
				signData.CertificateChains = [][]*x509.Certificate{{x509Cert, issuerX509}}
			}
		}
	}

	rdr, err := pdf.NewReader(bytes.NewReader(pdfContent), int64(len(pdfContent)))
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}

	var signedOut bytes.Buffer
	if err := sign.Sign(bytes.NewReader(pdfContent), &signedOut, rdr, int64(len(pdfContent)), signData); err != nil {
		return nil, fmt.Errorf("sign PDF: %w", err)
	}

	return signedOut.Bytes(), nil
}
