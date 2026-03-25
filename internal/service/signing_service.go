package service

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-signing/internal/data"
	pdfsign "github.com/go-tangra/go-tangra-signing/pkg/pdf/sign"
	pdfverify "github.com/go-tangra/go-tangra-signing/pkg/pdf/verify"
	securitycert "github.com/go-tangra/go-tangra-signing/pkg/security/cert"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type SigningService struct {
	signingV1.UnimplementedSigningDocumentServiceServer

	log       *log.Helper
	certRepo  *data.CertificateRepo
	storage   *data.StorageClient
	eventRepo *data.EventRepo
}

func NewSigningService(
	ctx *bootstrap.Context,
	certRepo *data.CertificateRepo,
	storage *data.StorageClient,
	eventRepo *data.EventRepo,
) *SigningService {
	return &SigningService{
		log:       ctx.NewLoggerHelper("signing/service/signing"),
		certRepo:  certRepo,
		storage:   storage,
		eventRepo: eventRepo,
	}
}

// SignDocument applies a digital signature to a PDF document.
func (s *SigningService) SignDocument(ctx context.Context, req *signingV1.SignDocumentRequest) (*signingV1.SignDocumentResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	// Get the signing certificate
	cert, err := s.certRepo.GetByID(ctx, req.CertificateId)
	if err != nil {
		return nil, err
	}
	if cert == nil {
		return nil, signingV1.ErrorCertificateNotFound("certificate not found")
	}

	// Tenant ownership check
	if derefUint32(cert.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Validate document key belongs to this tenant's storage paths
	if err := validateStorageKey(req.DocumentKey, tenantID); err != nil {
		return nil, err
	}

	// Parse certificate and private key
	x509Cert, err := securitycert.ParseCertificate([]byte(cert.CertPem))
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	privateKey, err := securitycert.ParsePrivateKeyAuto([]byte(cert.KeyPemEncrypted), s.certRepo.KEK())
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Download the PDF to sign
	pdfContent, err := s.storage.Download(ctx, req.DocumentKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download document: %w", err)
	}

	// Sign the PDF
	inputReader := bytes.NewReader(pdfContent)
	var outputBuffer bytes.Buffer

	signData := pdfsign.SignData{
		Certificate: x509Cert,
		Signer:      privateKey,
		Signature: pdfsign.SignDataSignature{
			CertType:   pdfsign.CertificationSignature,
			DocMDPPerm: pdfsign.AllowFillingExistingFormFieldsAndSignaturesPerms,
			Info: pdfsign.SignDataSignatureInfo{
				Name:        req.SignerName,
				Location:    req.Location,
				Reason:      req.Reason,
				ContactInfo: req.ContactInfo,
			},
		},
		RevocationFunction: pdfsign.DefaultEmbedRevocationStatusFunction,
	}

	// Build certificate chain if issuer exists
	if cert.ParentID != nil {
		issuerCert, err := s.certRepo.GetByID(ctx, *cert.ParentID)
		if err == nil && issuerCert != nil {
			issuerX509, err := securitycert.ParseCertificate([]byte(issuerCert.CertPem))
			if err == nil {
				signData.CertificateChains = [][]*x509.Certificate{{x509Cert, issuerX509}}
			}
		}
	}

	// Configure TSA if provided
	if req.TsaUrl != "" {
		signData.TSA = pdfsign.TSA{
			URL:      req.TsaUrl,
			Username: req.TsaUsername,
			Password: req.TsaPassword,
		}
	}

	err = pdfsign.Sign(inputReader, &outputBuffer, nil, int64(len(pdfContent)), signData)
	if err != nil {
		return nil, fmt.Errorf("failed to sign PDF: %w", err)
	}

	// Upload signed document
	signedKey := fmt.Sprintf("%d/signed/%s", tenantID, generateUUID()+".pdf")
	_, err = s.storage.Upload(ctx, tenantID, "signed", signedKey, outputBuffer.Bytes(), "application/pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to upload signed document: %w", err)
	}

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "document.signed", getUserIDFromContext(ctx),
		"document", req.DocumentKey, map[string]interface{}{
			"certificate_id": req.CertificateId,
			"signed_key":     signedKey,
		}, "")

	return &signingV1.SignDocumentResponse{
		SignedDocumentKey: signedKey,
	}, nil
}

// VerifyDocument verifies the digital signatures on a PDF document.
func (s *SigningService) VerifyDocument(ctx context.Context, req *signingV1.VerifyDocumentRequest) (*signingV1.VerifyDocumentResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	// Download the PDF to verify
	var pdfContent []byte
	var err error

	const maxContentSize = 50 * 1024 * 1024 // 50 MB
	if len(req.Content) > 0 {
		if len(req.Content) > maxContentSize {
			return nil, signingV1.ErrorBadRequest("document too large (max 50 MB)")
		}
		pdfContent = req.Content
	} else if req.DocumentKey != "" {
		// Validate document key belongs to this tenant
		if valErr := validateStorageKey(req.DocumentKey, tenantID); valErr != nil {
			return nil, valErr
		}
		pdfContent, err = s.storage.Download(ctx, req.DocumentKey)
		if err != nil {
			return nil, fmt.Errorf("failed to download document: %w", err)
		}
	} else {
		return nil, signingV1.ErrorBadRequest("either content or document_key must be provided")
	}

	// Verify signatures
	result, err := pdfverify.Verify(pdfContent)
	if err != nil {
		return nil, fmt.Errorf("failed to verify document: %w", err)
	}

	// Convert to proto response
	signers := make([]*signingV1.SignerInfo, 0, len(result.Signers))
	for _, signer := range result.Signers {
		signers = append(signers, &signingV1.SignerInfo{
			Name:               signer.Name,
			Reason:             signer.Reason,
			Location:           signer.Location,
			ContactInfo:        signer.ContactInfo,
			ValidSignature:     signer.ValidSignature,
			TrustedIssuer:      signer.TrustedIssuer,
			RevokedCertificate: signer.RevokedCertificate,
		})
	}

	return &signingV1.VerifyDocumentResponse{
		Signers: signers,
		DocumentInfo: &signingV1.DocumentInfo{
			Title:  result.DocumentInfo.Title,
			Author: result.DocumentInfo.Author,
			Pages:  int32(result.DocumentInfo.Pages),
		},
	}, nil
}
