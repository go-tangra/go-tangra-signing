package data

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/certificate"
	securitycert "github.com/go-tangra/go-tangra-signing/pkg/security/cert"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type CertificateRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
	kek       []byte // Server-side Key Encryption Key for CA/admin keys
}

func NewCertificateRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *CertificateRepo {
	repo := &CertificateRepo{
		log:       ctx.NewLoggerHelper("signing/certificate/repo"),
		entClient: entClient,
	}

	// Load KEK from environment (hex-encoded, 64 chars = 32 bytes)
	kekHex := os.Getenv("SIGNING_KEK")
	if kekHex != "" {
		kek, err := hex.DecodeString(kekHex)
		if err != nil || len(kek) != 32 {
			repo.log.Errorf("SIGNING_KEK must be 64 hex characters (32 bytes), got %d chars", len(kekHex))
		} else {
			repo.kek = kek
			repo.log.Info("KEK loaded for CA/admin key encryption")
		}
	} else {
		repo.log.Warn("SIGNING_KEK not set — CA/admin private keys will be stored unencrypted (NOT recommended for production)")
	}

	return repo
}

// KEK returns the server-side Key Encryption Key (may be nil if not configured).
func (r *CertificateRepo) KEK() []byte {
	return r.kek
}

// Create creates a new certificate record.
// The private key is encrypted with the server-side KEK if available, otherwise stored as plaintext PEM.
func (r *CertificateRepo) Create(ctx context.Context, tenantID uint32, subjectCN, subjectOrg string, result *securitycert.Result, privateKey *securitycert.PrivateKey, isCA bool, parentID string, createdBy *uint32) (*ent.Certificate, error) {
	id := uuid.New().String()

	// Encrypt the private key with KEK if available
	var keyPEM string
	if r.kek != nil {
		encrypted, err := securitycert.EncryptKeyWithKEK(privateKey.PrivateKey, r.kek)
		if err != nil {
			r.log.Errorf("failed to encrypt key with KEK: %s", err.Error())
			return nil, signingV1.ErrorInternalServerError("failed to encrypt private key")
		}
		keyPEM = encrypted
	} else {
		keyPEM = privateKey.String()
	}

	builder := r.entClient.Client().Certificate.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetSubjectCn(subjectCN).
		SetSerialNumber(fmt.Sprintf("%x", result.Cert.SerialNumber)).
		SetNotBefore(result.Cert.NotBefore).
		SetNotAfter(result.Cert.NotAfter).
		SetIsCa(isCA).
		SetCertPem(result.String()).
		SetKeyPemEncrypted(keyPEM).
		SetCreateTime(time.Now())

	if subjectOrg != "" {
		builder.SetSubjectOrg(subjectOrg)
	}
	if parentID != "" {
		builder.SetParentID(parentID)
	}
	if createdBy != nil {
		builder.SetCreateBy(*createdBy)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		r.log.Errorf("create certificate failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("create certificate failed")
	}

	return entity, nil
}

// GetByID retrieves a certificate by ID.
func (r *CertificateRepo) GetByID(ctx context.Context, id string) (*ent.Certificate, error) {
	entity, err := r.entClient.Client().Certificate.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get certificate failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get certificate failed")
	}
	return entity, nil
}

// List lists certificates with optional filters.
// createdBy filters to certificates created by a specific user (for signing:user role).
func (r *CertificateRepo) List(ctx context.Context, tenantID uint32, isCA *bool, status *string, createdBy *uint32, page, pageSize uint32) ([]*ent.Certificate, int, error) {
	query := r.entClient.Client().Certificate.Query().
		Where(certificate.TenantIDEQ(tenantID))

	if isCA != nil {
		query = query.Where(certificate.IsCaEQ(*isCA))
	}
	if status != nil && *status != "" {
		query = query.Where(certificate.StatusEQ(certificate.Status(*status)))
	}
	if createdBy != nil {
		query = query.Where(certificate.CreateByEQ(*createdBy))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		r.log.Errorf("count certificates failed: %s", err.Error())
		return nil, 0, signingV1.ErrorInternalServerError("count certificates failed")
	}

	if page > 0 && pageSize > 0 {
		offset := int((page - 1) * pageSize)
		query = query.Offset(offset).Limit(int(pageSize))
	}

	entities, err := query.Order(ent.Desc(certificate.FieldCreateTime)).All(ctx)
	if err != nil {
		r.log.Errorf("list certificates failed: %s", err.Error())
		return nil, 0, signingV1.ErrorInternalServerError("list certificates failed")
	}

	return entities, total, nil
}

// Revoke marks a certificate as revoked.
func (r *CertificateRepo) Revoke(ctx context.Context, id, reason string, revokedAt time.Time) (*ent.Certificate, error) {
	builder := r.entClient.Client().Certificate.UpdateOneID(id).
		SetStatus(certificate.StatusCERT_STATUS_REVOKED).
		SetRevokedAt(revokedAt)

	if reason != "" {
		builder.SetRevocationReason(reason)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, signingV1.ErrorCertificateNotFound("certificate not found")
		}
		r.log.Errorf("revoke certificate failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("revoke certificate failed")
	}

	return entity, nil
}

// UpdateCRL updates the CRL PEM on a CA certificate.
func (r *CertificateRepo) UpdateCRL(ctx context.Context, id, crlPem string) (*ent.Certificate, error) {
	entity, err := r.entClient.Client().Certificate.UpdateOneID(id).
		SetCrlPem(crlPem).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, signingV1.ErrorCertificateNotFound("certificate not found")
		}
		r.log.Errorf("update CRL failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("update CRL failed")
	}
	return entity, nil
}

// GetByEmail retrieves an active, completed certificate by tenant + email.
func (r *CertificateRepo) GetByEmail(ctx context.Context, tenantID uint32, email string) (*ent.Certificate, error) {
	entity, err := r.entClient.Client().Certificate.Query().
		Where(
			certificate.TenantIDEQ(tenantID),
			certificate.UserEmailEQ(email),
			certificate.StatusEQ(certificate.StatusCERT_STATUS_ACTIVE),
			certificate.SetupCompletedEQ(true),
		).
		Order(ent.Desc(certificate.FieldCreateTime)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get certificate by email failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get certificate by email failed")
	}
	return entity, nil
}

// GetBySetupToken retrieves a certificate by its setup token.
func (r *CertificateRepo) GetBySetupToken(ctx context.Context, token string) (*ent.Certificate, error) {
	entity, err := r.entClient.Client().Certificate.Query().
		Where(certificate.SetupTokenEQ(token)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get certificate by setup token failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get certificate by setup token failed")
	}
	return entity, nil
}

// GetTenantCA retrieves the active CA certificate for a tenant.
func (r *CertificateRepo) GetTenantCA(ctx context.Context, tenantID uint32) (*ent.Certificate, error) {
	entity, err := r.entClient.Client().Certificate.Query().
		Where(
			certificate.TenantIDEQ(tenantID),
			certificate.IsCaEQ(true),
			certificate.StatusEQ(certificate.StatusCERT_STATUS_ACTIVE),
		).
		Order(ent.Desc(certificate.FieldCreateTime)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get tenant CA failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get tenant CA failed")
	}
	return entity, nil
}

// CreatePending creates a certificate record with no key/cert yet (pending PIN setup).
func (r *CertificateRepo) CreatePending(ctx context.Context, tenantID uint32, subjectCN, subjectOrg, email, setupToken string, createdBy *uint32) (*ent.Certificate, error) {
	id := uuid.New().String()

	builder := r.entClient.Client().Certificate.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetSubjectCn(subjectCN).
		SetSubjectOrg(subjectOrg).
		SetSerialNumber(fmt.Sprintf("pending-%s", id)).
		SetNotBefore(time.Now()).
		SetNotAfter(time.Now()).
		SetCertPem("pending").
		SetUserEmail(email).
		SetSetupToken(setupToken).
		SetSetupCompleted(false).
		SetCreateTime(time.Now())

	if createdBy != nil {
		builder.SetCreateBy(*createdBy)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		r.log.Errorf("create pending certificate failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("create pending certificate failed")
	}

	return entity, nil
}

// CompleteSetup updates a pending certificate with the generated cert and encrypted key.
func (r *CertificateRepo) CompleteSetup(ctx context.Context, id string, result *securitycert.Result, encryptedKeyPEM string, isCA bool, parentID string) (*ent.Certificate, error) {
	// Parse the actual generated cert from DER to get the correct serial number.
	// result.Cert may point to the parent CA when issuer-signed, not the new cert.
	parsedCert, parseErr := x509.ParseCertificate(result.ByteCert)
	if parseErr != nil {
		r.log.Errorf("failed to parse generated certificate DER: %s", parseErr.Error())
		return nil, signingV1.ErrorInternalServerError("failed to parse generated certificate")
	}

	builder := r.entClient.Client().Certificate.UpdateOneID(id).
		SetSerialNumber(fmt.Sprintf("%x", parsedCert.SerialNumber)).
		SetNotBefore(parsedCert.NotBefore).
		SetNotAfter(parsedCert.NotAfter).
		SetCertPem(result.String()).
		SetKeyPemEncrypted(encryptedKeyPEM).
		SetIsCa(isCA).
		SetSetupCompleted(true)

	if parentID != "" {
		builder.SetParentID(parentID)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, signingV1.ErrorCertificateNotFound("certificate not found")
		}
		r.log.Errorf("complete certificate setup failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("complete certificate setup failed")
	}

	return entity, nil
}

// GetPendingByEmail retrieves a pending (not yet set up) certificate by tenant + email.
func (r *CertificateRepo) GetPendingByEmail(ctx context.Context, tenantID uint32, email string) (*ent.Certificate, error) {
	entity, err := r.entClient.Client().Certificate.Query().
		Where(
			certificate.TenantIDEQ(tenantID),
			certificate.UserEmailEQ(email),
			certificate.SetupCompletedEQ(false),
		).
		Order(ent.Desc(certificate.FieldCreateTime)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get pending certificate by email failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get pending certificate by email failed")
	}
	return entity, nil
}

// ToProto converts an ent.Certificate to signingV1.Certificate.
func (r *CertificateRepo) ToProto(entity *ent.Certificate) *signingV1.Certificate {
	if entity == nil {
		return nil
	}

	proto := &signingV1.Certificate{
		Id:           entity.ID,
		TenantId:     derefUint32(entity.TenantID),
		SubjectCn:    entity.SubjectCn,
		SubjectOrg:   entity.SubjectOrg,
		SerialNumber: entity.SerialNumber,
		NotBefore:    timestamppb.New(entity.NotBefore),
		NotAfter:     timestamppb.New(entity.NotAfter),
		IsCa:         entity.IsCa,
		Status:       entity.Status.String(),
		CertPem:      entity.CertPem,
	}

	if entity.ParentID != nil {
		proto.ParentId = entity.ParentID
	}
	if entity.RevokedAt != nil {
		proto.RevokedAt = timestamppb.New(*entity.RevokedAt)
	}
	if entity.CreateTime != nil && !entity.CreateTime.IsZero() {
		proto.CreateTime = timestamppb.New(*entity.CreateTime)
	}

	proto.UserEmail = entity.UserEmail
	proto.UserId = entity.UserID
	proto.SetupCompleted = entity.SetupCompleted

	return proto
}
