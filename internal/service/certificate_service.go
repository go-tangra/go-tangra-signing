package service

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-signing/internal/data"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	securitycert "github.com/go-tangra/go-tangra-signing/pkg/security/cert"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type CertificateService struct {
	signingV1.UnimplementedSigningCertificateServiceServer

	log      *log.Helper
	certRepo *data.CertificateRepo
}

func NewCertificateService(
	ctx *bootstrap.Context,
	certRepo *data.CertificateRepo,
) *CertificateService {
	return &CertificateService{
		log:      ctx.NewLoggerHelper("signing/service/certificate"),
		certRepo: certRepo,
	}
}

// CreateCertificate generates a new X.509 certificate.
func (s *CertificateService) CreateCertificate(ctx context.Context, req *signingV1.CreateCertificateRequest) (*signingV1.CreateCertificateResponse, error) {
	tenantID := getTenantIDFromContext(ctx)
	createdBy := getUserIDAsUint32(ctx)

	// Generate ECDSA P-256 private key
	privateKey, err := securitycert.GetPrivateKey()
	if err != nil {
		return nil, signingV1.ErrorInternalServerError("failed to generate private key")
	}

	// Build certificate configuration
	certConfig := &securitycert.Certificate{
		Subject: pkix.Name{
			CommonName:   req.SubjectCn,
			Organization: []string{req.SubjectOrg},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(int(req.ValidityYears), 0, 0),
		IsCA:      req.IsCa,
	}

	if req.IsCa {
		certConfig.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
		certConfig.ExtentedKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
	} else {
		certConfig.KeyUsage = x509.KeyUsageDigitalSignature
		certConfig.ExtentedKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}
	}

	// If parent CA specified, use it as issuer
	if req.ParentId != "" {
		parentCert, err := s.certRepo.GetByID(ctx, req.ParentId)
		if err != nil {
			return nil, err
		}
		if parentCert == nil {
			return nil, signingV1.ErrorCertificateNotFound("parent CA not found")
		}

		// Tenant ownership check on parent CA
		if derefUint32(parentCert.TenantID) != tenantID {
			return nil, signingV1.ErrorAccessDenied("access denied")
		}

		// Parse parent certificate and key
		parentX509, err := securitycert.ParseCertificate([]byte(parentCert.CertPem))
		if err != nil {
			return nil, signingV1.ErrorInternalServerError("failed to parse parent certificate")
		}
		parentPrivKey, err := securitycert.ParsePrivateKeyAuto([]byte(parentCert.KeyPemEncrypted), s.certRepo.KEK())
		if err != nil {
			return nil, signingV1.ErrorInternalServerError("failed to parse parent key")
		}

		certConfig.Parent = parentX509
		certConfig.ParentPrivateKey = parentPrivKey
	}

	// Generate the certificate
	result, err := certConfig.GetCertificate(privateKey.PrivateKey)
	if err != nil {
		return nil, signingV1.ErrorInternalServerError("failed to generate certificate")
	}

	// Store in database
	entity, err := s.certRepo.Create(ctx, tenantID,
		req.SubjectCn, req.SubjectOrg,
		result, privateKey,
		req.IsCa, req.ParentId,
		createdBy,
	)
	if err != nil {
		return nil, err
	}

	return &signingV1.CreateCertificateResponse{
		Certificate: s.certRepo.ToProto(entity),
	}, nil
}

// GetCertificate gets a certificate by ID.
func (s *CertificateService) GetCertificate(ctx context.Context, req *signingV1.GetCertificateRequest) (*signingV1.GetCertificateResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	cert, err := s.certRepo.GetByID(ctx, req.Id)
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

	return &signingV1.GetCertificateResponse{
		Certificate: s.certRepo.ToProto(cert),
	}, nil
}

// ListCertificates lists certificates with pagination.
func (s *CertificateService) ListCertificates(ctx context.Context, req *signingV1.ListCertificatesRequest) (*signingV1.ListCertificatesResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	page := uint32(1)
	if req.Page != nil {
		page = *req.Page
	}
	pageSize := uint32(20)
	if req.PageSize != nil {
		pageSize = *req.PageSize
	}

	// Signing users can only see their own certificates
	var createdByFilter *uint32
	if isSigningUser(ctx) {
		createdByFilter = getUserIDAsUint32(ctx)
	}

	certs, total, err := s.certRepo.List(ctx, tenantID, req.IsCa, req.Status, createdByFilter, page, pageSize)
	if err != nil {
		return nil, err
	}

	protoCerts := make([]*signingV1.Certificate, 0, len(certs))
	for _, c := range certs {
		protoCerts = append(protoCerts, s.certRepo.ToProto(c))
	}

	return &signingV1.ListCertificatesResponse{
		Certificates: protoCerts,
		Total:        uint32(total),
	}, nil
}

// RevokeCertificate revokes a certificate and updates the CRL.
func (s *CertificateService) RevokeCertificate(ctx context.Context, req *signingV1.RevokeCertificateRequest) (*signingV1.RevokeCertificateResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	cert, err := s.certRepo.GetByID(ctx, req.Id)
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

	// Update certificate status
	now := time.Now()
	cert, err = s.certRepo.Revoke(ctx, req.Id, req.Reason, now)
	if err != nil {
		return nil, err
	}

	// Update CRL on the issuer certificate if available
	if cert.ParentID != nil {
		s.updateCRL(ctx, *cert.ParentID, cert)
	}

	return &signingV1.RevokeCertificateResponse{
		Certificate: s.certRepo.ToProto(cert),
	}, nil
}

// updateCRL regenerates the CRL for a CA certificate by adding the revoked
// certificate to the issuer's Certificate Revocation List and persisting it.
func (s *CertificateService) updateCRL(ctx context.Context, caID string, revokedCert *ent.Certificate) {
	// Load the CA certificate
	caCert, err := s.certRepo.GetByID(ctx, caID)
	if err != nil || caCert == nil {
		s.log.Errorf("failed to load CA certificate %s for CRL update: %v", caID, err)
		return
	}

	// Parse the CA certificate and private key
	caX509, err := securitycert.ParseCertificate([]byte(caCert.CertPem))
	if err != nil {
		s.log.Errorf("failed to parse CA certificate for CRL update: %v", err)
		return
	}
	caPrivKey, err := securitycert.ParsePrivateKeyAuto([]byte(caCert.KeyPemEncrypted), s.certRepo.KEK())
	if err != nil {
		s.log.Errorf("failed to parse CA private key for CRL update: %v", err)
		return
	}

	// Parse the revoked certificate
	revokedX509, err := securitycert.ParseCertificate([]byte(revokedCert.CertPem))
	if err != nil {
		s.log.Errorf("failed to parse revoked certificate for CRL update: %v", err)
		return
	}

	// Decode existing CRL from PEM (may be empty for first revocation)
	var existingCRLDER []byte
	if caCert.CrlPem != "" {
		block, _ := pem.Decode([]byte(caCert.CrlPem))
		if block != nil {
			existingCRLDER = block.Bytes
		}
	}

	// Generate updated CRL with the newly revoked certificate
	nextUpdate := time.Now().AddDate(0, 0, 30) // CRL valid for 30 days
	updatedCRL, _, err := securitycert.RevokeCertificate(existingCRLDER, revokedX509, caX509, caPrivKey, nextUpdate)
	if err != nil {
		s.log.Errorf("failed to generate updated CRL for CA %s: %v", caID, err)
		return
	}

	// PEM-encode the DER CRL bytes for storage
	crlPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "X509 CRL",
		Bytes: updatedCRL.Byte,
	})

	// Persist the updated CRL on the CA certificate
	_, err = s.certRepo.UpdateCRL(ctx, caID, string(crlPEM))
	if err != nil {
		s.log.Errorf("failed to persist updated CRL for CA %s: %v", caID, err)
		return
	}

	s.log.Infof("CRL updated for CA %s after revoking certificate %s", caID, revokedCert.ID)
}
