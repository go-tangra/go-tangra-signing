package service

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-common/backup"
	"github.com/go-tangra/go-tangra-common/grpcx"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/certificate"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/event"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submission"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submitter"
	entTemplate "github.com/go-tangra/go-tangra-signing/internal/data/ent/template"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/templatefolder"
)

const (
	backupModule        = "signing"
	backupSchemaVersion = 1
)

// Migrations registry — add entries here when schema changes.
var migrations = backup.NewMigrationRegistry(backupModule)

// Register migrations in init. Example for future use:
//
//	func init() {
//	    migrations.Register(1, func(entities map[string]json.RawMessage) error {
//	        return backup.MigrateAddField(entities, "templates", "newField", "")
//	    })
//	}

// BackupService handles backup export and import for the signing module.
type BackupService struct {
	signingV1.UnimplementedBackupServiceServer

	log       *log.Helper
	entClient *entCrud.EntClient[*ent.Client]
}

// NewBackupService creates a new BackupService instance.
func NewBackupService(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *BackupService {
	return &BackupService{
		log:       ctx.NewLoggerHelper("signing/service/backup"),
		entClient: entClient,
	}
}

// ExportBackup exports all signing entities as a gzipped archive.
func (s *BackupService) ExportBackup(ctx context.Context, req *signingV1.ExportBackupRequest) (*signingV1.ExportBackupResponse, error) {
	tenantID := grpcx.GetTenantIDFromContext(ctx)
	full := false

	if grpcx.IsPlatformAdmin(ctx) && req.TenantId != nil && *req.TenantId == 0 {
		full = true
		tenantID = 0
	} else if req.TenantId != nil && *req.TenantId != 0 && grpcx.IsPlatformAdmin(ctx) {
		tenantID = *req.TenantId
	}

	client := s.entClient.Client()
	a := backup.NewArchive(backupModule, backupSchemaVersion, tenantID, full)

	// Export template folders
	if err := s.exportTemplateFolders(ctx, client, a, tenantID, full); err != nil {
		return nil, err
	}

	// Export templates
	if err := s.exportTemplates(ctx, client, a, tenantID, full); err != nil {
		return nil, err
	}

	// Export certificates (conditionally include secrets)
	if err := s.exportCertificates(ctx, client, a, tenantID, full, req.GetIncludeSecrets()); err != nil {
		return nil, err
	}

	// Export submissions
	if err := s.exportSubmissions(ctx, client, a, tenantID, full); err != nil {
		return nil, err
	}

	// Export submitters
	if err := s.exportSubmitters(ctx, client, a, tenantID, full); err != nil {
		return nil, err
	}

	// Export events
	if err := s.exportEvents(ctx, client, a, tenantID, full); err != nil {
		return nil, err
	}

	// Pack (JSON + gzip)
	data, err := backup.Pack(a)
	if err != nil {
		return nil, fmt.Errorf("pack backup: %w", err)
	}

	s.log.Infof("exported backup: module=%s tenant=%d full=%v entities=%v", backupModule, tenantID, full, a.Manifest.EntityCounts)

	return &signingV1.ExportBackupResponse{
		Data:          data,
		Module:        backupModule,
		Version:       fmt.Sprintf("%d", backupSchemaVersion),
		ExportedAt:    timestamppb.New(a.Manifest.ExportedAt),
		TenantId:      tenantID,
		EntityCounts:  a.Manifest.EntityCounts,
		SchemaVersion: int32(backupSchemaVersion),
	}, nil
}

// ImportBackup restores signing entities from a gzipped archive.
func (s *BackupService) ImportBackup(ctx context.Context, req *signingV1.ImportBackupRequest) (*signingV1.ImportBackupResponse, error) {
	tenantID := grpcx.GetTenantIDFromContext(ctx)
	isPlatformAdmin := grpcx.IsPlatformAdmin(ctx)
	mode := mapRestoreMode(req.GetMode())

	// Unpack
	a, err := backup.Unpack(req.GetData())
	if err != nil {
		return nil, fmt.Errorf("unpack backup: %w", err)
	}

	// Validate
	if err := backup.Validate(a, backupModule, backupSchemaVersion); err != nil {
		return nil, err
	}

	// Full backups require platform admin
	if a.Manifest.FullBackup && !isPlatformAdmin {
		return nil, fmt.Errorf("only platform admins can restore full backups")
	}

	// Run migrations if needed
	sourceVersion := a.Manifest.SchemaVersion
	applied, err := migrations.RunMigrations(a, backupSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	// Determine restore tenant
	if !isPlatformAdmin || !a.Manifest.FullBackup {
		tenantID = grpcx.GetTenantIDFromContext(ctx)
	} else {
		tenantID = 0
	}

	client := s.entClient.Client()
	result := backup.NewRestoreResult(sourceVersion, backupSchemaVersion, applied)

	// Import in FK dependency order:
	// 1. Template folders (self-referential parent_id)
	s.importTemplateFolders(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)

	// 2. Templates (depends on template folders)
	s.importTemplates(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)

	// 3. Certificates (no FK deps within signing)
	s.importCertificates(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)

	// 4. Submissions (depends on templates)
	s.importSubmissions(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)

	// 5. Submitters (depends on submissions)
	s.importSubmitters(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)

	// 6. Events (depends on submissions)
	s.importEvents(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)

	s.log.Infof("imported backup: module=%s tenant=%d mode=%v migrations=%d results=%d",
		backupModule, tenantID, mode, applied, len(result.Results))

	// Convert to proto response
	protoResults := make([]*signingV1.EntityImportResult, len(result.Results))
	for i, r := range result.Results {
		protoResults[i] = &signingV1.EntityImportResult{
			EntityType: r.EntityType,
			Total:      r.Total,
			Created:    r.Created,
			Updated:    r.Updated,
			Skipped:    r.Skipped,
			Failed:     r.Failed,
		}
	}

	return &signingV1.ImportBackupResponse{
		Success:           result.Success,
		Results:           protoResults,
		Warnings:          result.Warnings,
		SourceVersion:     int32(result.SourceVersion),
		TargetVersion:     int32(result.TargetVersion),
		MigrationsApplied: int32(result.MigrationsApplied),
	}, nil
}

func mapRestoreMode(m signingV1.RestoreMode) backup.RestoreMode {
	if m == signingV1.RestoreMode_RESTORE_MODE_OVERWRITE {
		return backup.RestoreModeOverwrite
	}
	return backup.RestoreModeSkip
}

// --- Export helpers ---

func (s *BackupService) exportTemplateFolders(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool) error {
	q := client.TemplateFolder.Query()
	if !full {
		q = q.Where(templatefolder.TenantID(tenantID))
	}
	folders, err := q.All(ctx)
	if err != nil {
		return fmt.Errorf("export template folders: %w", err)
	}
	if err := backup.SetEntities(a, "templateFolders", folders); err != nil {
		return fmt.Errorf("set template folders: %w", err)
	}
	return nil
}

func (s *BackupService) exportTemplates(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool) error {
	q := client.Template.Query()
	if !full {
		q = q.Where(entTemplate.TenantID(tenantID))
	}
	templates, err := q.All(ctx)
	if err != nil {
		return fmt.Errorf("export templates: %w", err)
	}
	if err := backup.SetEntities(a, "templates", templates); err != nil {
		return fmt.Errorf("set templates: %w", err)
	}
	return nil
}

func (s *BackupService) exportCertificates(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, includeSecrets bool) error {
	q := client.Certificate.Query()
	if !full {
		q = q.Where(certificate.TenantID(tenantID))
	}
	certs, err := q.All(ctx)
	if err != nil {
		return fmt.Errorf("export certificates: %w", err)
	}

	// Strip sensitive key material unless explicitly requested
	if !includeSecrets {
		stripped := make([]*ent.Certificate, len(certs))
		for i, c := range certs {
			cp := *c
			cp.KeyPemEncrypted = ""
			stripped[i] = &cp
		}
		certs = stripped
	}

	if err := backup.SetEntities(a, "certificates", certs); err != nil {
		return fmt.Errorf("set certificates: %w", err)
	}
	return nil
}

func (s *BackupService) exportSubmissions(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool) error {
	q := client.Submission.Query()
	if !full {
		q = q.Where(submission.TenantID(tenantID))
	}
	submissions, err := q.All(ctx)
	if err != nil {
		return fmt.Errorf("export submissions: %w", err)
	}
	if err := backup.SetEntities(a, "submissions", submissions); err != nil {
		return fmt.Errorf("set submissions: %w", err)
	}
	return nil
}

func (s *BackupService) exportSubmitters(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool) error {
	q := client.Submitter.Query()
	if !full {
		q = q.Where(submitter.TenantID(tenantID))
	}
	submitters, err := q.All(ctx)
	if err != nil {
		return fmt.Errorf("export submitters: %w", err)
	}
	if err := backup.SetEntities(a, "submitters", submitters); err != nil {
		return fmt.Errorf("set submitters: %w", err)
	}
	return nil
}

func (s *BackupService) exportEvents(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool) error {
	q := client.Event.Query()
	if !full {
		q = q.Where(event.TenantID(tenantID))
	}
	events, err := q.All(ctx)
	if err != nil {
		return fmt.Errorf("export events: %w", err)
	}
	if err := backup.SetEntities(a, "events", events); err != nil {
		return fmt.Errorf("set events: %w", err)
	}
	return nil
}

// --- Import helpers ---

// topologicalSortByParentID sorts items so parents come before children.
func topologicalSortByParentID[T any](items []T, getID func(T) string, getParentID func(T) string) []T {
	idSet := make(map[string]bool, len(items))
	for _, item := range items {
		idSet[getID(item)] = true
	}

	childMap := make(map[string][]T)
	var roots []T
	for _, item := range items {
		pid := getParentID(item)
		if pid == "" || !idSet[pid] {
			roots = append(roots, item)
		} else {
			childMap[pid] = append(childMap[pid], item)
		}
	}

	sorted := make([]T, 0, len(items))
	var walk func([]T)
	walk = func(nodes []T) {
		for _, n := range nodes {
			sorted = append(sorted, n)
			if children, ok := childMap[getID(n)]; ok {
				walk(children)
			}
		}
	}
	walk(roots)
	return sorted
}

func (s *BackupService) importTemplateFolders(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	folders, err := backup.GetEntities[ent.TemplateFolder](a, "templateFolders")
	if err != nil {
		result.AddWarning(fmt.Sprintf("templateFolders: unmarshal error: %v", err))
		return
	}
	if len(folders) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "templateFolders", Total: int64(len(folders))}

	// Topological sort for self-referential parent_id
	sorted := topologicalSortByParentID(folders,
		func(e ent.TemplateFolder) string { return e.ID },
		func(e ent.TemplateFolder) string {
			if e.ParentID == nil {
				return ""
			}
			return *e.ParentID
		},
	)

	for _, e := range sorted {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.TemplateFolder.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("templateFolders: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.TemplateFolder.UpdateOneID(e.ID).
				SetNillableParentID(e.ParentID).
				SetName(e.Name).
				SetPath(e.Path).
				SetDepth(e.Depth).
				SetSortOrder(e.SortOrder).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("templateFolders: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.TemplateFolder.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetNillableParentID(e.ParentID).
				SetName(e.Name).
				SetPath(e.Path).
				SetDepth(e.Depth).
				SetSortOrder(e.SortOrder).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("templateFolders: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importTemplates(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	templates, err := backup.GetEntities[ent.Template](a, "templates")
	if err != nil {
		result.AddWarning(fmt.Sprintf("templates: unmarshal error: %v", err))
		return
	}
	if len(templates) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "templates", Total: int64(len(templates))}

	for _, e := range templates {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Template.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("templates: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.Template.UpdateOneID(e.ID).
				SetNillableFolderID(e.FolderID).
				SetName(e.Name).
				SetSlug(e.Slug).
				SetDescription(e.Description).
				SetFileKey(e.FileKey).
				SetFileName(e.FileName).
				SetFileSize(e.FileSize).
				SetFields(e.Fields).
				SetStatus(e.Status).
				SetSource(e.Source).
				SetTags(e.Tags).
				SetNillableCreateBy(e.CreateBy).
				SetNillableUpdateBy(e.UpdateBy).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("templates: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.Template.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetNillableFolderID(e.FolderID).
				SetName(e.Name).
				SetSlug(e.Slug).
				SetDescription(e.Description).
				SetFileKey(e.FileKey).
				SetFileName(e.FileName).
				SetFileSize(e.FileSize).
				SetFields(e.Fields).
				SetStatus(e.Status).
				SetSource(e.Source).
				SetTags(e.Tags).
				SetNillableCreateBy(e.CreateBy).
				SetNillableUpdateBy(e.UpdateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("templates: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importCertificates(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	certs, err := backup.GetEntities[ent.Certificate](a, "certificates")
	if err != nil {
		result.AddWarning(fmt.Sprintf("certificates: unmarshal error: %v", err))
		return
	}
	if len(certs) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "certificates", Total: int64(len(certs))}

	// Topological sort for self-referential parent_id (CA chain)
	sorted := topologicalSortByParentID(certs,
		func(e ent.Certificate) string { return e.ID },
		func(e ent.Certificate) string {
			if e.ParentID == nil {
				return ""
			}
			return *e.ParentID
		},
	)

	for _, e := range sorted {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Certificate.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("certificates: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.Certificate.UpdateOneID(e.ID).
				SetSubjectCn(e.SubjectCn).
				SetSubjectOrg(e.SubjectOrg).
				SetSerialNumber(e.SerialNumber).
				SetNotBefore(e.NotBefore).
				SetNotAfter(e.NotAfter).
				SetIsCa(e.IsCa).
				SetNillableParentID(e.ParentID).
				SetNillableKeyPemEncrypted(&e.KeyPemEncrypted).
				SetCertPem(e.CertPem).
				SetCrlPem(e.CrlPem).
				SetStatus(e.Status).
				SetKeyAlgorithm(e.KeyAlgorithm).
				SetNillableRevokedAt(e.RevokedAt).
				SetRevocationReason(e.RevocationReason).
				SetUserEmail(e.UserEmail).
				SetUserID(e.UserID).
				SetSetupToken(e.SetupToken).
				SetSetupCompleted(e.SetupCompleted).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("certificates: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.Certificate.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetSubjectCn(e.SubjectCn).
				SetSubjectOrg(e.SubjectOrg).
				SetSerialNumber(e.SerialNumber).
				SetNotBefore(e.NotBefore).
				SetNotAfter(e.NotAfter).
				SetIsCa(e.IsCa).
				SetNillableParentID(e.ParentID).
				SetNillableKeyPemEncrypted(&e.KeyPemEncrypted).
				SetCertPem(e.CertPem).
				SetCrlPem(e.CrlPem).
				SetStatus(e.Status).
				SetKeyAlgorithm(e.KeyAlgorithm).
				SetNillableRevokedAt(e.RevokedAt).
				SetRevocationReason(e.RevocationReason).
				SetUserEmail(e.UserEmail).
				SetUserID(e.UserID).
				SetSetupToken(e.SetupToken).
				SetSetupCompleted(e.SetupCompleted).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("certificates: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importSubmissions(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	submissions, err := backup.GetEntities[ent.Submission](a, "submissions")
	if err != nil {
		result.AddWarning(fmt.Sprintf("submissions: unmarshal error: %v", err))
		return
	}
	if len(submissions) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "submissions", Total: int64(len(submissions))}

	for _, e := range submissions {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Submission.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("submissions: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.Submission.UpdateOneID(e.ID).
				SetTemplateID(e.TemplateID).
				SetSlug(e.Slug).
				SetSigningMode(e.SigningMode).
				SetStatus(e.Status).
				SetTemplateFieldsSnapshot(e.TemplateFieldsSnapshot).
				SetTemplateSchemaSnapshot(e.TemplateSchemaSnapshot).
				SetTemplateSubmittersSnapshot(e.TemplateSubmittersSnapshot).
				SetPreferences(e.Preferences).
				SetNillableCompletedAt(e.CompletedAt).
				SetNillableExpiresAt(e.ExpiresAt).
				SetSource(e.Source).
				SetCurrentPdfKey(e.CurrentPdfKey).
				SetSignedDocumentKey(e.SignedDocumentKey).
				SetAuditTrailKey(e.AuditTrailKey).
				SetNillableCreateBy(e.CreateBy).
				SetNillableUpdateBy(e.UpdateBy).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("submissions: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.Submission.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetTemplateID(e.TemplateID).
				SetSlug(e.Slug).
				SetSigningMode(e.SigningMode).
				SetStatus(e.Status).
				SetTemplateFieldsSnapshot(e.TemplateFieldsSnapshot).
				SetTemplateSchemaSnapshot(e.TemplateSchemaSnapshot).
				SetTemplateSubmittersSnapshot(e.TemplateSubmittersSnapshot).
				SetPreferences(e.Preferences).
				SetNillableCompletedAt(e.CompletedAt).
				SetNillableExpiresAt(e.ExpiresAt).
				SetSource(e.Source).
				SetCurrentPdfKey(e.CurrentPdfKey).
				SetSignedDocumentKey(e.SignedDocumentKey).
				SetAuditTrailKey(e.AuditTrailKey).
				SetNillableCreateBy(e.CreateBy).
				SetNillableUpdateBy(e.UpdateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("submissions: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importSubmitters(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	submitters, err := backup.GetEntities[ent.Submitter](a, "submitters")
	if err != nil {
		result.AddWarning(fmt.Sprintf("submitters: unmarshal error: %v", err))
		return
	}
	if len(submitters) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "submitters", Total: int64(len(submitters))}

	for _, e := range submitters {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Submitter.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("submitters: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.Submitter.UpdateOneID(e.ID).
				SetSubmissionID(e.SubmissionID).
				SetName(e.Name).
				SetEmail(e.Email).
				SetPhone(e.Phone).
				SetSlug(e.Slug).
				SetSigningOrder(e.SigningOrder).
				SetRole(e.Role).
				SetStatus(e.Status).
				SetValues(e.Values).
				SetMetadata(e.Metadata).
				SetIP(e.IP).
				SetUserAgent(e.UserAgent).
				SetDeclineReason(e.DeclineReason).
				SetNillableSentAt(e.SentAt).
				SetNillableOpenedAt(e.OpenedAt).
				SetNillableCompletedAt(e.CompletedAt).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("submitters: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.Submitter.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetSubmissionID(e.SubmissionID).
				SetName(e.Name).
				SetEmail(e.Email).
				SetPhone(e.Phone).
				SetSlug(e.Slug).
				SetSigningOrder(e.SigningOrder).
				SetRole(e.Role).
				SetStatus(e.Status).
				SetValues(e.Values).
				SetMetadata(e.Metadata).
				SetIP(e.IP).
				SetUserAgent(e.UserAgent).
				SetDeclineReason(e.DeclineReason).
				SetNillableSentAt(e.SentAt).
				SetNillableOpenedAt(e.OpenedAt).
				SetNillableCompletedAt(e.CompletedAt).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("submitters: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importEvents(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	events, err := backup.GetEntities[ent.Event](a, "events")
	if err != nil {
		result.AddWarning(fmt.Sprintf("events: unmarshal error: %v", err))
		return
	}
	if len(events) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "events", Total: int64(len(events))}

	for _, e := range events {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Event.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("events: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.Event.UpdateOneID(e.ID).
				SetEventType(e.EventType).
				SetActorID(e.ActorID).
				SetResourceType(e.ResourceType).
				SetResourceID(e.ResourceID).
				SetSubmissionID(e.SubmissionID).
				SetMetadata(e.Metadata).
				SetIP(e.IP).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("events: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.Event.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetEventType(e.EventType).
				SetActorID(e.ActorID).
				SetResourceType(e.ResourceType).
				SetResourceID(e.ResourceID).
				SetSubmissionID(e.SubmissionID).
				SetMetadata(e.Metadata).
				SetIP(e.IP).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("events: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}
