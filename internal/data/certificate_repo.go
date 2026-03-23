package data

import (
	"context"
	"fmt"
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
}

func NewCertificateRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *CertificateRepo {
	return &CertificateRepo{
		log:       ctx.NewLoggerHelper("signing/certificate/repo"),
		entClient: entClient,
	}
}

// Create creates a new certificate record.
func (r *CertificateRepo) Create(ctx context.Context, tenantID uint32, subjectCN, subjectOrg string, result *securitycert.Result, privateKey *securitycert.PrivateKey, isCA bool, parentID string, createdBy *uint32) (*ent.Certificate, error) {
	id := uuid.New().String()

	builder := r.entClient.Client().Certificate.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetSubjectCn(subjectCN).
		SetSerialNumber(fmt.Sprintf("%x", result.Cert.SerialNumber)).
		SetNotBefore(result.Cert.NotBefore).
		SetNotAfter(result.Cert.NotAfter).
		SetIsCa(isCA).
		SetCertPem(result.String()).
		SetKeyPemEncrypted(privateKey.String()).
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
func (r *CertificateRepo) List(ctx context.Context, tenantID uint32, isCA *bool, status *string, page, pageSize uint32) ([]*ent.Certificate, int, error) {
	query := r.entClient.Client().Certificate.Query().
		Where(certificate.TenantIDEQ(tenantID))

	if isCA != nil {
		query = query.Where(certificate.IsCaEQ(*isCA))
	}
	if status != nil && *status != "" {
		query = query.Where(certificate.StatusEQ(certificate.Status(*status)))
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

	return proto
}
