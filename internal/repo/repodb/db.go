// Package repodb binds repo.Store to TimescaleDB via *store.Store. Tenant
// methods run in a tenant transaction (RLS) — the caller's when invoked inside
// Tx, else their own; *System methods run under the system scope. LockSubmission
// takes the submission row lock (SELECT … FOR UPDATE) so parallel signers are
// serialised; RecordPINFailure always commits in its own transaction so a
// rolled-back signing still counts the failure. Unique and foreign-key
// violations map to repo.ErrConflict and malformed ids to repo.ErrNotFound.
package repodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// DB implements repo.Store over *store.Store.
type DB struct {
	St *store.Store
	tx pgx.Tx // set inside Tx
}

var _ repo.Store = (*DB)(nil)

// New wraps the store.
func New(st *store.Store) *DB { return &DB{St: st} }

func (d *DB) run(ctx context.Context, tenantID string, fn func(tx pgx.Tx) error) error {
	if d.tx != nil {
		return fn(d.tx)
	}
	if tenantID == "" {
		return repo.ErrNotFound
	}
	return d.St.Tx(ctx, store.Scope{TenantID: tenantID}, fn)
}

func (d *DB) system(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return d.St.Tx(ctx, store.Scope{System: true}, fn)
}

// Tx implements repo.Store.
func (d *DB) Tx(ctx context.Context, tenantID string, fn func(repo.Store) error) error {
	if d.tx != nil {
		return fn(d)
	}
	if tenantID == "" {
		return repo.ErrNotFound
	}
	return mapErr(d.St.Tx(ctx, store.Scope{TenantID: tenantID}, func(tx pgx.Tx) error {
		return fn(&DB{St: d.St, tx: tx})
	}))
}

type scanner interface{ Scan(dest ...any) error }

// mapErr maps store errors: no row / malformed id → ErrNotFound, unique or
// foreign-key violation → ErrConflict. Sentinels pass through.
func mapErr(err error) error {
	if err == nil || errors.Is(err, repo.ErrNotFound) || errors.Is(err, repo.ErrConflict) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return repo.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505", "23503": // unique_violation, foreign_key_violation
			return repo.ErrConflict
		case "22P02": // invalid_text_representation (a non-uuid id)
			return repo.ErrNotFound
		}
	}
	return err
}

func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(q)) + "%"
}

func js(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func unjs(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// pageClause is the ORDER BY / LIMIT / OFFSET of a list page, clamped to the
// last page for total. Built only from Spec constants and integers.
func pageClause(spec listquery.Spec, req listquery.Request, total int) string {
	req = req.Clamp(total)
	return fmt.Sprintf(` ORDER BY %s LIMIT %d OFFSET %d`, req.OrderBy(spec), req.Limit(), req.Offset())
}

func affected(tag pgconn.CommandTag) error {
	if tag.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- folders

const folderCols = `id, tenant_id, parent_id, name, path, sort_order, created_at, created_by, updated_at, updated_by`

func scanFolder(sc scanner) (f store.Folder, err error) {
	err = sc.Scan(&f.ID, &f.TenantID, &f.ParentID, &f.Name, &f.Path, &f.SortOrder, &f.CreatedAt, &f.CreatedBy, &f.UpdatedAt, &f.UpdatedBy)
	return f, err
}

// CreateFolder implements repo.Store.
func (d *DB) CreateFolder(ctx context.Context, f store.Folder) error {
	return mapErr(d.run(ctx, f.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_template_folders (`+folderCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			f.ID, f.TenantID, f.ParentID, f.Name, f.Path, f.SortOrder, orNow(f.CreatedAt), f.CreatedBy, orNow(f.UpdatedAt), f.UpdatedBy)
		return err
	}))
}

func orNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

// GetFolder implements repo.Store.
func (d *DB) GetFolder(ctx context.Context, tenantID, id string) (f store.Folder, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		f, err = scanFolder(tx.QueryRow(ctx, `SELECT `+folderCols+` FROM signing_template_folders WHERE tenant_id = $1 AND id = $2`, tenantID, id))
		return err
	})
	return f, mapErr(err)
}

// ListFolders implements repo.Store.
func (d *DB) ListFolders(ctx context.Context, tenantID string) (out []store.Folder, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+folderCols+` FROM signing_template_folders WHERE tenant_id = $1 ORDER BY path, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFolder(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// UpdateFolder implements repo.Store.
func (d *DB) UpdateFolder(ctx context.Context, f store.Folder) error {
	return mapErr(d.run(ctx, f.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_template_folders SET parent_id=$3, name=$4, path=$5, sort_order=$6, updated_at=$7, updated_by=$8
			WHERE tenant_id=$1 AND id=$2`, f.TenantID, f.ID, f.ParentID, f.Name, f.Path, f.SortOrder, orNow(f.UpdatedAt), f.UpdatedBy)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// RewriteFolderPaths implements repo.Store.
func (d *DB) RewriteFolderPaths(ctx context.Context, tenantID, oldPrefix, newPrefix string) error {
	return mapErr(d.run(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE signing_template_folders SET path = $3 || substr(path, char_length($2) + 1)
			WHERE tenant_id = $1 AND left(path, char_length($2) + 1) = $2 || '/'`, tenantID, oldPrefix, newPrefix)
		return err
	}))
}

// FolderInUse implements repo.Store.
func (d *DB) FolderInUse(ctx context.Context, tenantID, id string) (used bool, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signing_template_folders WHERE tenant_id=$1 AND parent_id=$2)
			OR EXISTS (SELECT 1 FROM signing_templates WHERE tenant_id=$1 AND folder_id=$2)`, tenantID, id).Scan(&used)
	})
	return used, mapErr(err)
}

// DeleteFolder implements repo.Store.
func (d *DB) DeleteFolder(ctx context.Context, tenantID, id string) error {
	return mapErr(d.run(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM signing_template_folders WHERE tenant_id=$1 AND id=$2`, tenantID, id)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// ---------------------------------------------------------------- templates

const templateCols = `id, tenant_id, folder_id, name, description, tags, status, pdf_key, pdf_sha256, pdf_size, pdf_pages, file_name,
 pdf_signed, parties, fields, default_expiry_days, default_reminder, version, created_at, created_by, updated_at, updated_by`

func scanTemplate(sc scanner) (t store.Template, err error) {
	var parties, fields, reminder []byte
	err = sc.Scan(&t.ID, &t.TenantID, &t.FolderID, &t.Name, &t.Description, &t.Tags, &t.Status, &t.PDFKey, &t.PDFSHA256, &t.PDFSize,
		&t.PDFPages, &t.FileName, &t.PDFSigned, &parties, &fields, &t.DefaultExpiryDays, &reminder, &t.Version, &t.CreatedAt, &t.CreatedBy,
		&t.UpdatedAt, &t.UpdatedBy)
	if err != nil {
		return t, err
	}
	if err := unjs(parties, &t.Parties); err != nil {
		return t, err
	}
	if err := unjs(fields, &t.Fields); err != nil {
		return t, err
	}
	if len(reminder) > 0 && string(reminder) != "null" {
		t.DefaultReminder = &store.Reminder{}
		if err := unjs(reminder, t.DefaultReminder); err != nil {
			return t, err
		}
	}
	return t, nil
}

func reminderJSON(r *store.Reminder) []byte {
	if r == nil {
		return nil
	}
	return js(r)
}

func tagsOf(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

// CreateTemplate implements repo.Store.
func (d *DB) CreateTemplate(ctx context.Context, t store.Template) error {
	if t.Version == 0 {
		t.Version = 1
	}
	return mapErr(d.run(ctx, t.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_templates (`+templateCols+`)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
			t.ID, t.TenantID, t.FolderID, t.Name, t.Description, tagsOf(t.Tags), t.Status, t.PDFKey, t.PDFSHA256, t.PDFSize, t.PDFPages,
			t.FileName, t.PDFSigned, js(t.Parties), js(t.Fields), t.DefaultExpiryDays, reminderJSON(t.DefaultReminder), t.Version,
			orNow(t.CreatedAt), t.CreatedBy, orNow(t.UpdatedAt), t.UpdatedBy)
		return err
	}))
}

// GetTemplate implements repo.Store.
func (d *DB) GetTemplate(ctx context.Context, tenantID, id string) (t store.Template, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		t, err = scanTemplate(tx.QueryRow(ctx, `SELECT `+templateCols+` FROM signing_templates WHERE tenant_id=$1 AND id=$2`, tenantID, id))
		return err
	})
	return t, mapErr(err)
}

// ListTemplates implements repo.Store.
func (d *DB) ListTemplates(ctx context.Context, tenantID string, f repo.TemplateFilter) (out []store.Template, total int, err error) {
	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.FolderID != nil {
		if *f.FolderID == "" {
			where = append(where, "folder_id IS NULL")
		} else {
			add("folder_id = $%d", *f.FolderID)
		}
	}
	if f.Tag != "" {
		add("EXISTS (SELECT 1 FROM unnest(tags) tg WHERE lower(tg) = lower($%d))", f.Tag)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Query != "" {
		add(`lower(name) LIKE $%d ESCAPE '\'`, likePattern(f.Query))
	}
	w := strings.Join(where, " AND ")
	spec, req := f.List()
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM signing_templates WHERE `+w, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+templateCols+` FROM signing_templates WHERE `+w+pageClause(spec, req, total), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTemplate(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, total, mapErr(err)
}

// UpdateTemplate implements repo.Store.
func (d *DB) UpdateTemplate(ctx context.Context, t store.Template, expectedVersion int) (out store.Template, err error) {
	err = d.run(ctx, t.TenantID, func(tx pgx.Tx) error {
		out, err = scanTemplate(tx.QueryRow(ctx, `UPDATE signing_templates SET folder_id=$4, name=$5, description=$6, tags=$7, status=$8,
			pdf_key=$9, pdf_sha256=$10, pdf_size=$11, pdf_pages=$12, file_name=$13, pdf_signed=$14, parties=$15, fields=$16,
			default_expiry_days=$17, default_reminder=$18, version = version + 1, updated_at=$19, updated_by=$20
			WHERE tenant_id=$1 AND id=$2 AND version=$3 RETURNING `+templateCols,
			t.TenantID, t.ID, expectedVersion, t.FolderID, t.Name, t.Description, tagsOf(t.Tags), t.Status, t.PDFKey, t.PDFSHA256, t.PDFSize,
			t.PDFPages, t.FileName, t.PDFSigned, js(t.Parties), js(t.Fields), t.DefaultExpiryDays, reminderJSON(t.DefaultReminder),
			orNow(t.UpdatedAt), t.UpdatedBy))
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signing_templates WHERE tenant_id=$1 AND id=$2)`, t.TenantID, t.ID).Scan(&exists); e != nil {
				return e
			}
			if exists {
				return repo.ErrConflict
			}
		}
		return err
	})
	return out, mapErr(err)
}

// TemplateInUse implements repo.Store.
func (d *DB) TemplateInUse(ctx context.Context, tenantID, id string) (used bool, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signing_submissions WHERE tenant_id=$1 AND template_id=$2
			AND status IN ('draft','in_progress'))`, tenantID, id).Scan(&used)
	})
	return used, mapErr(err)
}

// DeleteTemplate implements repo.Store (a referenced template is a FK conflict).
func (d *DB) DeleteTemplate(ctx context.Context, tenantID, id string) error {
	return mapErr(d.run(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM signing_templates WHERE tenant_id=$1 AND id=$2`, tenantID, id)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// ---------------------------------------------------------------- submissions

const submissionCols = `id, tenant_id, template_id, name, pdf_key, pdf_sha256, fields, parties, mode, status, expires_at, reminder,
 current_version, final_version, audit_trail_key, audit_trail_sha256, sent_at, completed_at, cancelled_at, cancel_reason,
 created_at, created_by, updated_at, source, source_ref, idempotency_key`

func scanSubmission(sc scanner) (s store.Submission, err error) {
	var fields, parties, reminder []byte
	err = sc.Scan(&s.ID, &s.TenantID, &s.TemplateID, &s.Name, &s.PDFKey, &s.PDFSHA256, &fields, &parties, &s.Mode, &s.Status, &s.ExpiresAt,
		&reminder, &s.CurrentVersion, &s.FinalVersion, &s.AuditTrailKey, &s.AuditTrailSHA256, &s.SentAt, &s.CompletedAt, &s.CancelledAt,
		&s.CancelReason, &s.CreatedAt, &s.CreatedBy, &s.UpdatedAt, &s.Source, &s.SourceRef, &s.IdempotencyKey)
	if err != nil {
		return s, err
	}
	if err := unjs(fields, &s.Fields); err != nil {
		return s, err
	}
	if err := unjs(parties, &s.Parties); err != nil {
		return s, err
	}
	if len(reminder) > 0 && string(reminder) != "null" {
		s.Reminder = &store.Reminder{}
		if err := unjs(reminder, s.Reminder); err != nil {
			return s, err
		}
	}
	return s, nil
}

const signerCols = `id, tenant_id, submission_id, user_id, name, email, party, position, status, "values", method, certificate_id,
 cert_subject, cert_serial, cert_issuer, ip, user_agent, decline_reason, invited_at, opened_at, signed_at, declined_at,
 reminders_sent, next_reminder_at, mail_error`

func scanSigner(sc scanner) (s store.Signer, err error) {
	var values []byte
	err = sc.Scan(&s.ID, &s.TenantID, &s.SubmissionID, &s.UserID, &s.Name, &s.Email, &s.Party, &s.Position, &s.Status, &values, &s.Method,
		&s.CertificateID, &s.CertSubject, &s.CertSerial, &s.CertIssuer, &s.IP, &s.UserAgent, &s.DeclineReason, &s.InvitedAt, &s.OpenedAt,
		&s.SignedAt, &s.DeclinedAt, &s.RemindersSent, &s.NextReminderAt, &s.MailError)
	if err != nil {
		return s, err
	}
	s.Values = map[string]string{}
	return s, unjs(values, &s.Values)
}

func valuesOf(v map[string]string) []byte {
	if v == nil {
		return []byte("{}")
	}
	return js(v)
}

func insertSigner(ctx context.Context, tx pgx.Tx, s store.Signer) error {
	_, err := tx.Exec(ctx, `INSERT INTO signing_signers (`+signerCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`,
		s.ID, s.TenantID, s.SubmissionID, s.UserID, s.Name, s.Email, s.Party, s.Position, s.Status, valuesOf(s.Values), s.Method,
		s.CertificateID, s.CertSubject, s.CertSerial, s.CertIssuer, s.IP, s.UserAgent, s.DeclineReason, s.InvitedAt, s.OpenedAt, s.SignedAt,
		s.DeclinedAt, s.RemindersSent, s.NextReminderAt, s.MailError)
	return err
}

func insertVersion(ctx context.Context, tx pgx.Tx, v store.DocumentVersion) error {
	_, err := tx.Exec(ctx, `INSERT INTO signing_document_versions (submission_id, version, tenant_id, object_key, sha256, size, signer_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, v.SubmissionID, v.Version, v.TenantID, v.ObjectKey, v.SHA256, v.Size, v.SignerID, orNow(v.CreatedAt))
	return err
}

// CreateSubmission implements repo.Store. The template must belong to the
// submission's tenant (RLS makes a foreign template invisible).
func (d *DB) CreateSubmission(ctx context.Context, s store.Submission, signers []store.Signer, v0 store.DocumentVersion) error {
	return mapErr(d.run(ctx, s.TenantID, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signing_templates WHERE tenant_id=$1 AND id=$2)`, s.TenantID, s.TemplateID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return repo.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `INSERT INTO signing_submissions (`+submissionCols+`)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
			s.ID, s.TenantID, s.TemplateID, s.Name, s.PDFKey, s.PDFSHA256, js(s.Fields), js(s.Parties), s.Mode, s.Status, s.ExpiresAt,
			reminderJSON(s.Reminder), s.CurrentVersion, s.FinalVersion, s.AuditTrailKey, s.AuditTrailSHA256, s.SentAt, s.CompletedAt,
			s.CancelledAt, s.CancelReason, orNow(s.CreatedAt), s.CreatedBy, orNow(s.UpdatedAt), s.Source, s.SourceRef,
			s.IdempotencyKey); err != nil {
			return err
		}
		for _, sg := range signers {
			if err := insertSigner(ctx, tx, sg); err != nil {
				return err
			}
		}
		return insertVersion(ctx, tx, v0)
	}))
}

func signersOf(ctx context.Context, tx pgx.Tx, tenantID, subID string) (out []store.Signer, err error) {
	rows, err := tx.Query(ctx, `SELECT `+signerCols+` FROM signing_signers WHERE tenant_id=$1 AND submission_id=$2 ORDER BY position, id`, tenantID, subID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSigner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) getSubmission(ctx context.Context, tenantID, id, lock string) (s store.Submission, sg []store.Signer, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		s, err = scanSubmission(tx.QueryRow(ctx, `SELECT `+submissionCols+` FROM signing_submissions WHERE tenant_id=$1 AND id=$2`+lock, tenantID, id))
		if err != nil {
			return err
		}
		sg, err = signersOf(ctx, tx, tenantID, id)
		return err
	})
	return s, sg, mapErr(err)
}

// GetSubmission implements repo.Store.
func (d *DB) GetSubmission(ctx context.Context, tenantID, id string) (store.Submission, []store.Signer, error) {
	return d.getSubmission(ctx, tenantID, id, "")
}

// SubmissionByIdempotency implements repo.Store.
func (d *DB) SubmissionByIdempotency(ctx context.Context, tenantID, source, key string) (s store.Submission, sg []store.Signer, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		s, err = scanSubmission(tx.QueryRow(ctx, `SELECT `+submissionCols+` FROM signing_submissions
			WHERE tenant_id=$1 AND source=$2 AND idempotency_key=$3 AND idempotency_key <> ''`, tenantID, source, key))
		if err != nil {
			return err
		}
		sg, err = signersOf(ctx, tx, tenantID, s.ID)
		return err
	})
	return s, sg, mapErr(err)
}

// LockSubmission implements repo.Store.
func (d *DB) LockSubmission(ctx context.Context, tenantID, id string) (store.Submission, []store.Signer, error) {
	return d.getSubmission(ctx, tenantID, id, " FOR UPDATE")
}

// ListSubmissions implements repo.Store.
func (d *DB) ListSubmissions(ctx context.Context, tenantID string, f repo.SubmissionFilter) (out []store.Submission, total int, err error) {
	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.TemplateID != "" {
		add("template_id = $%d", f.TemplateID)
	}
	if f.CreatedBy != "" {
		add("created_by = $%d", f.CreatedBy)
	}
	if f.Query != "" {
		add(`lower(name) LIKE $%d ESCAPE '\'`, likePattern(f.Query))
	}
	w := strings.Join(where, " AND ")
	spec, req := f.List()
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM signing_submissions WHERE `+w, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+submissionCols+` FROM signing_submissions WHERE `+w+pageClause(spec, req, total), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			s, err := scanSubmission(rows)
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, total, mapErr(err)
}

// UpdateSubmission implements repo.Store.
func (d *DB) UpdateSubmission(ctx context.Context, s store.Submission) error {
	return mapErr(d.run(ctx, s.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_submissions SET name=$3, fields=$4, parties=$5, mode=$6, status=$7, expires_at=$8,
			reminder=$9, current_version=$10, final_version=$11, audit_trail_key=$12, audit_trail_sha256=$13, sent_at=$14, completed_at=$15,
			cancelled_at=$16, cancel_reason=$17, updated_at=$18 WHERE tenant_id=$1 AND id=$2`,
			s.TenantID, s.ID, s.Name, js(s.Fields), js(s.Parties), s.Mode, s.Status, s.ExpiresAt, reminderJSON(s.Reminder), s.CurrentVersion,
			s.FinalVersion, s.AuditTrailKey, s.AuditTrailSHA256, s.SentAt, s.CompletedAt, s.CancelledAt, s.CancelReason, orNow(s.UpdatedAt))
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// UpdateSigner implements repo.Store.
func (d *DB) UpdateSigner(ctx context.Context, s store.Signer) error {
	return mapErr(d.run(ctx, s.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_signers SET user_id=$3, name=$4, email=$5, party=$6, position=$7, status=$8, "values"=$9,
			method=$10, certificate_id=$11, cert_subject=$12, cert_serial=$13, cert_issuer=$14, ip=$15, user_agent=$16, decline_reason=$17,
			invited_at=$18, opened_at=$19, signed_at=$20, declined_at=$21, reminders_sent=$22, next_reminder_at=$23, mail_error=$24
			WHERE tenant_id=$1 AND id=$2`,
			s.TenantID, s.ID, s.UserID, s.Name, s.Email, s.Party, s.Position, s.Status, valuesOf(s.Values), s.Method, s.CertificateID,
			s.CertSubject, s.CertSerial, s.CertIssuer, s.IP, s.UserAgent, s.DeclineReason, s.InvitedAt, s.OpenedAt, s.SignedAt, s.DeclinedAt,
			s.RemindersSent, s.NextReminderAt, s.MailError)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// DeleteSubmission implements repo.Store (signers, versions, events, jobs and
// preparations cascade).
func (d *DB) DeleteSubmission(ctx context.Context, tenantID, id string) error {
	return mapErr(d.run(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM signing_submissions WHERE tenant_id=$1 AND id=$2`, tenantID, id)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// GetSigner implements repo.Store.
func (d *DB) GetSigner(ctx context.Context, tenantID, id string) (s store.Signer, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		s, err = scanSigner(tx.QueryRow(ctx, `SELECT `+signerCols+` FROM signing_signers WHERE tenant_id=$1 AND id=$2`, tenantID, id))
		return err
	})
	return s, mapErr(err)
}

// Inbox implements repo.Store.
func (d *DB) Inbox(ctx context.Context, tenantID, userID string, f repo.InboxFilter) (out []repo.InboxItem, total int, err error) {
	cond := `sg.status IN ('invited','opened') AND s.status = 'in_progress'`
	if f.Signed {
		cond = `sg.status = 'signed'`
	}
	spec, req := f.List()
	sgCols := "sg." + strings.ReplaceAll(strings.ReplaceAll(signerCols, "\n", ""), ", ", ", sg.")
	sCols := "s." + strings.ReplaceAll(strings.ReplaceAll(submissionCols, "\n", ""), ", ", ", s.")
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		from := ` FROM signing_signers sg JOIN signing_submissions s ON s.id = sg.submission_id
			WHERE sg.tenant_id = $1 AND sg.user_id = $2 AND ` + cond
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from, tenantID, userID).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+sgCols+`, `+sCols+from+pageClause(spec, req, total), tenantID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it repo.InboxItem
			var values, fields, parties, reminder []byte
			s := &it.Submission
			sg := &it.Signer
			if err := rows.Scan(&sg.ID, &sg.TenantID, &sg.SubmissionID, &sg.UserID, &sg.Name, &sg.Email, &sg.Party, &sg.Position, &sg.Status,
				&values, &sg.Method, &sg.CertificateID, &sg.CertSubject, &sg.CertSerial, &sg.CertIssuer, &sg.IP, &sg.UserAgent, &sg.DeclineReason,
				&sg.InvitedAt, &sg.OpenedAt, &sg.SignedAt, &sg.DeclinedAt, &sg.RemindersSent, &sg.NextReminderAt, &sg.MailError,
				&s.ID, &s.TenantID, &s.TemplateID, &s.Name, &s.PDFKey, &s.PDFSHA256, &fields, &parties, &s.Mode, &s.Status, &s.ExpiresAt,
				&reminder, &s.CurrentVersion, &s.FinalVersion, &s.AuditTrailKey, &s.AuditTrailSHA256, &s.SentAt, &s.CompletedAt, &s.CancelledAt,
				&s.CancelReason, &s.CreatedAt, &s.CreatedBy, &s.UpdatedAt, &s.Source, &s.SourceRef, &s.IdempotencyKey); err != nil {
				return err
			}
			sg.Values = map[string]string{}
			if err := unjs(values, &sg.Values); err != nil {
				return err
			}
			if err := unjs(fields, &s.Fields); err != nil {
				return err
			}
			if err := unjs(parties, &s.Parties); err != nil {
				return err
			}
			if len(reminder) > 0 && string(reminder) != "null" {
				s.Reminder = &store.Reminder{}
				if err := unjs(reminder, s.Reminder); err != nil {
					return err
				}
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	return out, total, mapErr(err)
}

// AddVersion implements repo.Store.
func (d *DB) AddVersion(ctx context.Context, v store.DocumentVersion) error {
	return mapErr(d.run(ctx, v.TenantID, func(tx pgx.Tx) error { return insertVersion(ctx, tx, v) }))
}

const versionCols = `submission_id, version, tenant_id, object_key, sha256, size, signer_id, created_at`

func scanVersion(sc scanner) (v store.DocumentVersion, err error) {
	err = sc.Scan(&v.SubmissionID, &v.Version, &v.TenantID, &v.ObjectKey, &v.SHA256, &v.Size, &v.SignerID, &v.CreatedAt)
	return v, err
}

// GetVersion implements repo.Store.
func (d *DB) GetVersion(ctx context.Context, tenantID, submissionID string, version int) (v store.DocumentVersion, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		v, err = scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM signing_document_versions WHERE tenant_id=$1 AND submission_id=$2 AND version=$3`,
			tenantID, submissionID, version))
		return err
	})
	return v, mapErr(err)
}

// ListVersions implements repo.Store.
func (d *DB) ListVersions(ctx context.Context, tenantID, submissionID string) (out []store.DocumentVersion, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+versionCols+` FROM signing_document_versions WHERE tenant_id=$1 AND submission_id=$2 ORDER BY version`,
			tenantID, submissionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanVersion(rows)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// ---------------------------------------------------------------- certificates

const certCols = `id, tenant_id, kind, issuer_id, owner_user_id, subject_cn, email, serial, not_before, not_after, cert_der, key_protection,
 key_blob, status, revoked_at, revocation_reason, failed_pin_count, locked_until, crl_der, crl_next_update, superseded_by, created_at, created_by`

func scanCert(sc scanner) (c store.Certificate, err error) {
	err = sc.Scan(&c.ID, &c.TenantID, &c.Kind, &c.IssuerID, &c.OwnerUserID, &c.SubjectCN, &c.Email, &c.Serial, &c.NotBefore, &c.NotAfter,
		&c.CertDER, &c.KeyProtection, &c.KeyBlob, &c.Status, &c.RevokedAt, &c.RevocationReason, &c.FailedPINCount, &c.LockedUntil,
		&c.CRLDER, &c.CRLNextUpdate, &c.SupersededBy, &c.CreatedAt, &c.CreatedBy)
	return c, err
}

func queryCerts(ctx context.Context, tx pgx.Tx, sql string, args ...any) (out []store.Certificate, err error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCertificate implements repo.Store.
func (d *DB) CreateCertificate(ctx context.Context, c store.Certificate) error {
	return mapErr(d.run(ctx, c.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_certificates (`+certCols+`)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
			c.ID, c.TenantID, c.Kind, c.IssuerID, c.OwnerUserID, c.SubjectCN, c.Email, c.Serial, c.NotBefore, c.NotAfter, c.CertDER,
			c.KeyProtection, c.KeyBlob, c.Status, c.RevokedAt, c.RevocationReason, c.FailedPINCount, c.LockedUntil, c.CRLDER,
			c.CRLNextUpdate, c.SupersededBy, orNow(c.CreatedAt), c.CreatedBy)
		return err
	}))
}

func (d *DB) oneCert(ctx context.Context, tenantID, where string, args ...any) (c store.Certificate, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		c, err = scanCert(tx.QueryRow(ctx, `SELECT `+certCols+` FROM signing_certificates WHERE tenant_id=$1 AND `+where, args...))
		return err
	})
	return c, mapErr(err)
}

// GetCertificate implements repo.Store.
func (d *DB) GetCertificate(ctx context.Context, tenantID, id string) (store.Certificate, error) {
	return d.oneCert(ctx, tenantID, `id=$2`, tenantID, id)
}

// ListCertificates implements repo.Store.
func (d *DB) ListCertificates(ctx context.Context, tenantID string, f repo.CertificateFilter) (out []store.Certificate, total int, err error) {
	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "$?", fmt.Sprintf("$%d", len(args))))
	}
	if f.Kind != "" {
		add("kind = $?", f.Kind)
	}
	if f.Status != "" {
		add("status = $?", f.Status)
	}
	if f.OwnerID != "" {
		add("owner_user_id = $?", f.OwnerID)
	}
	if f.Query != "" {
		add(`(lower(subject_cn) LIKE $? ESCAPE '\' OR lower(email) LIKE $? ESCAPE '\')`, likePattern(f.Query))
	}
	w := strings.Join(where, " AND ")
	spec, req := f.List()
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM signing_certificates WHERE `+w, args...).Scan(&total); err != nil {
			return err
		}
		out, err = queryCerts(ctx, tx, `SELECT `+certCols+` FROM signing_certificates WHERE `+w+pageClause(spec, req, total), args...)
		return err
	})
	return out, total, mapErr(err)
}

// UpdateCertificate implements repo.Store.
func (d *DB) UpdateCertificate(ctx context.Context, c store.Certificate) error {
	return mapErr(d.run(ctx, c.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_certificates SET subject_cn=$3, email=$4, key_blob=$5, status=$6, revoked_at=$7,
			revocation_reason=$8, failed_pin_count=$9, locked_until=$10, crl_der=$11, crl_next_update=$12, superseded_by=$13
			WHERE tenant_id=$1 AND id=$2`, c.TenantID, c.ID, c.SubjectCN, c.Email, c.KeyBlob, c.Status, c.RevokedAt, c.RevocationReason,
			c.FailedPINCount, c.LockedUntil, c.CRLDER, c.CRLNextUpdate, c.SupersededBy)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// ActiveSignerCertificate implements repo.Store.
func (d *DB) ActiveSignerCertificate(ctx context.Context, tenantID, userID string) (store.Certificate, error) {
	return d.oneCert(ctx, tenantID, `kind='signer' AND status='active' AND owner_user_id=$2`, tenantID, userID)
}

// CurrentCA implements repo.Store.
func (d *DB) CurrentCA(ctx context.Context, tenantID string) (store.Certificate, error) {
	return d.oneCert(ctx, tenantID, `kind='ca' AND status='active' AND superseded_by IS NULL ORDER BY not_after DESC LIMIT 1`, tenantID)
}

// ListCAs implements repo.Store.
func (d *DB) ListCAs(ctx context.Context, tenantID string) (out []store.Certificate, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		out, err = queryCerts(ctx, tx, `SELECT `+certCols+` FROM signing_certificates WHERE tenant_id=$1 AND kind='ca' ORDER BY not_before, id`, tenantID)
		return err
	})
	return out, mapErr(err)
}

// IssuedBy implements repo.Store.
func (d *DB) IssuedBy(ctx context.Context, tenantID, issuerID string) (out []store.Certificate, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		out, err = queryCerts(ctx, tx, `SELECT `+certCols+` FROM signing_certificates WHERE tenant_id=$1 AND issuer_id=$2 ORDER BY id`, tenantID, issuerID)
		return err
	})
	return out, mapErr(err)
}

// SystemCertificate implements repo.Store.
func (d *DB) SystemCertificate(ctx context.Context, tenantID, issuerID string) (store.Certificate, error) {
	return d.oneCert(ctx, tenantID, `kind='system' AND status='active' AND issuer_id=$2 ORDER BY not_after DESC LIMIT 1`, tenantID, issuerID)
}

// RecordPINFailure implements repo.Store (always its own transaction).
func (d *DB) RecordPINFailure(ctx context.Context, tenantID, certID string, lockAfter int, lockUntil time.Time) (n int, locked bool, err error) {
	err = d.St.Tx(ctx, store.Scope{TenantID: tenantID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `UPDATE signing_certificates SET failed_pin_count = failed_pin_count + 1
			WHERE tenant_id=$1 AND id=$2 RETURNING failed_pin_count`, tenantID, certID).Scan(&n); err != nil {
			return err
		}
		if n >= lockAfter {
			locked = true
			_, err := tx.Exec(ctx, `UPDATE signing_certificates SET failed_pin_count = 0, locked_until = $3 WHERE tenant_id=$1 AND id=$2`,
				tenantID, certID, lockUntil)
			return err
		}
		return nil
	})
	return n, locked, mapErr(err)
}

// ResetPINFailures implements repo.Store.
func (d *DB) ResetPINFailures(ctx context.Context, tenantID, certID string) error {
	return mapErr(d.run(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_certificates SET failed_pin_count = 0, locked_until = NULL WHERE tenant_id=$1 AND id=$2`, tenantID, certID)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// SignedWithCertificate implements repo.Store.
func (d *DB) SignedWithCertificate(ctx context.Context, tenantID, certID string, limit int) (out []repo.SignedDocument, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sg.submission_id, s.name, sg.id, sg.signed_at FROM signing_signers sg
			JOIN signing_submissions s ON s.id = sg.submission_id
			WHERE sg.tenant_id=$1 AND sg.certificate_id=$2 AND sg.signed_at IS NOT NULL ORDER BY sg.signed_at DESC LIMIT $3`, tenantID, certID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sd repo.SignedDocument
			if err := rows.Scan(&sd.SubmissionID, &sd.SubmissionName, &sd.SignerID, &sd.SignedAt); err != nil {
				return err
			}
			out = append(out, sd)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// ---------------------------------------------------------------- QES

const qesCols = `id, tenant_id, signer_id, chain_der, signed_attrs, digest, prepared_key, based_on_version, "values", expires_at, used_at, created_at`

func scanQES(sc scanner) (q store.QESPreparation, err error) {
	var values []byte
	err = sc.Scan(&q.ID, &q.TenantID, &q.SignerID, &q.ChainDER, &q.SignedAttrs, &q.Digest, &q.PreparedKey, &q.BasedOnVersion, &values,
		&q.ExpiresAt, &q.UsedAt, &q.CreatedAt)
	if err != nil {
		return q, err
	}
	q.Values = map[string]string{}
	return q, unjs(values, &q.Values)
}

// CreateQES implements repo.Store.
func (d *DB) CreateQES(ctx context.Context, q store.QESPreparation) error {
	return mapErr(d.run(ctx, q.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_qes_preparations (`+qesCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			q.ID, q.TenantID, q.SignerID, q.ChainDER, q.SignedAttrs, q.Digest, q.PreparedKey, q.BasedOnVersion, valuesOf(q.Values),
			q.ExpiresAt, q.UsedAt, orNow(q.CreatedAt))
		return err
	}))
}

// GetQES implements repo.Store.
func (d *DB) GetQES(ctx context.Context, tenantID, id string) (q store.QESPreparation, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		q, err = scanQES(tx.QueryRow(ctx, `SELECT `+qesCols+` FROM signing_qes_preparations WHERE tenant_id=$1 AND id=$2`, tenantID, id))
		return err
	})
	return q, mapErr(err)
}

// MarkQESUsed implements repo.Store.
func (d *DB) MarkQESUsed(ctx context.Context, tenantID, id string, at time.Time) error {
	return mapErr(d.run(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_qes_preparations SET used_at=$3 WHERE tenant_id=$1 AND id=$2 AND used_at IS NULL`, tenantID, id, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signing_qes_preparations WHERE tenant_id=$1 AND id=$2)`, tenantID, id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return repo.ErrConflict
			}
			return repo.ErrNotFound
		}
		return nil
	}))
}

// ---------------------------------------------------------------- history

// AddEvent implements repo.Store.
func (d *DB) AddEvent(ctx context.Context, e store.Event) error {
	if e.Meta == nil {
		e.Meta = map[string]any{}
	}
	return mapErr(d.run(ctx, e.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_events (id, tenant_id, submission_id, signer_id, certificate_id, actor_user_id, type, ip, meta, at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, e.ID, e.TenantID, e.SubmissionID, e.SignerID, e.CertificateID, e.ActorUserID, e.Type,
			e.IP, js(e.Meta), orNow(e.At))
		return err
	}))
}

// ListEvents implements repo.Store.
func (d *DB) ListEvents(ctx context.Context, tenantID, submissionID string) (out []store.Event, err error) {
	err = d.run(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, tenant_id, submission_id, signer_id, certificate_id, actor_user_id, type, ip, meta, at
			FROM signing_events WHERE tenant_id=$1 AND submission_id=$2 ORDER BY at, id`, tenantID, submissionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e store.Event
			var meta []byte
			if err := rows.Scan(&e.ID, &e.TenantID, &e.SubmissionID, &e.SignerID, &e.CertificateID, &e.ActorUserID, &e.Type, &e.IP, &meta, &e.At); err != nil {
				return err
			}
			e.Meta = map[string]any{}
			if err := unjs(meta, &e.Meta); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// ---------------------------------------------------------------- jobs

// EnqueueJob implements repo.Store (one job per submission and kind).
func (d *DB) EnqueueJob(ctx context.Context, j store.Job) error {
	return mapErr(d.run(ctx, j.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_jobs (id, tenant_id, kind, submission_id, attempts, next_attempt_at, last_error, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (submission_id, kind) DO NOTHING`,
			j.ID, j.TenantID, j.Kind, j.SubmissionID, j.Attempts, orNow(j.NextAttemptAt), j.LastError, orNow(j.CreatedAt))
		return err
	}))
}

const jobCols = `id, tenant_id, kind, submission_id, attempts, next_attempt_at, last_error, done_at, created_at`

// ClaimJobsSystem implements repo.Store (SKIP LOCKED: several replicas never claim the same job).
func (d *DB) ClaimJobsSystem(ctx context.Context, now time.Time, limit int, lease time.Duration) (out []store.Job, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE signing_jobs SET attempts = attempts + 1, next_attempt_at = $2
			WHERE id IN (SELECT id FROM signing_jobs WHERE done_at IS NULL AND next_attempt_at <= $1
			ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED) RETURNING `+jobCols, now, now.Add(lease), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j store.Job
			if err := rows.Scan(&j.ID, &j.TenantID, &j.Kind, &j.SubmissionID, &j.Attempts, &j.NextAttemptAt, &j.LastError, &j.DoneAt, &j.CreatedAt); err != nil {
				return err
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// CompleteJobSystem implements repo.Store.
func (d *DB) CompleteJobSystem(ctx context.Context, id string, at time.Time) error {
	return mapErr(d.system(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_jobs SET done_at=$2 WHERE id=$1`, id, at)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// FailJobSystem implements repo.Store.
func (d *DB) FailJobSystem(ctx context.Context, id, reason string, next time.Time) error {
	return mapErr(d.system(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE signing_jobs SET last_error=$2, next_attempt_at=$3 WHERE id=$1`, id, reason, next)
		if err != nil {
			return err
		}
		return affected(tag)
	}))
}

// ---------------------------------------------------------------- scheduled work

// ExpireDueSystem implements repo.Store.
func (d *DB) ExpireDueSystem(ctx context.Context, now time.Time, limit int) (out []store.Submission, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE signing_submissions SET status='expired', updated_at=$1
			WHERE id IN (SELECT id FROM signing_submissions WHERE status='in_progress' AND expires_at IS NOT NULL AND expires_at <= $1
			ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED) RETURNING `+submissionCols, now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			s, err := scanSubmission(rows)
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// ClaimRemindersSystem implements repo.Store: one UPDATE claims each due
// signer once per interval (next_reminder_at moves forward, or clears at the
// maximum or when the submission is no longer in progress).
func (d *DB) ClaimRemindersSystem(ctx context.Context, now time.Time, limit int) (out []repo.ReminderDue, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE signing_signers sg SET next_reminder_at = NULL FROM signing_submissions s
			WHERE s.id = sg.submission_id AND sg.next_reminder_at <= $1 AND sg.status IN ('invited','opened')
			AND (s.status <> 'in_progress' OR s.reminder IS NULL)`, now); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `WITH due AS (
				SELECT sg.id, (s.reminder->>'interval_days')::int AS days, (s.reminder->>'max')::int AS mx
				FROM signing_signers sg JOIN signing_submissions s ON s.id = sg.submission_id
				WHERE sg.next_reminder_at <= $1 AND sg.status IN ('invited','opened') AND s.status = 'in_progress' AND s.reminder IS NOT NULL
				ORDER BY sg.id LIMIT $2 FOR UPDATE OF sg SKIP LOCKED)
			UPDATE signing_signers sg SET reminders_sent = sg.reminders_sent + 1,
				next_reminder_at = CASE WHEN sg.reminders_sent + 1 >= due.mx THEN NULL ELSE $1 + make_interval(days => due.days) END
			FROM due WHERE sg.id = due.id RETURNING sg.id`, now, limit)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			sg, err := scanSigner(tx.QueryRow(ctx, `SELECT `+signerCols+` FROM signing_signers WHERE id=$1`, id))
			if err != nil {
				return err
			}
			s, err := scanSubmission(tx.QueryRow(ctx, `SELECT `+submissionCols+` FROM signing_submissions WHERE id=$1`, sg.SubmissionID))
			if err != nil {
				return err
			}
			out = append(out, repo.ReminderDue{Signer: sg, Submission: s, Number: sg.RemindersSent})
		}
		return nil
	})
	return out, mapErr(err)
}

// ExpiredQESSystem implements repo.Store.
func (d *DB) ExpiredQESSystem(ctx context.Context, now time.Time, limit int) (out []store.QESPreparation, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+qesCols+` FROM signing_qes_preparations WHERE expires_at <= $1 OR used_at IS NOT NULL
			ORDER BY id LIMIT $2`, now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			q, err := scanQES(rows)
			if err != nil {
				return err
			}
			out = append(out, q)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// DeleteQESSystem implements repo.Store.
func (d *DB) DeleteQESSystem(ctx context.Context, id string) error {
	return mapErr(d.system(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM signing_qes_preparations WHERE id=$1`, id)
		return err
	}))
}

// DueCRLsSystem implements repo.Store.
func (d *DB) DueCRLsSystem(ctx context.Context, now time.Time, limit int) (out []store.Certificate, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		out, err = queryCerts(ctx, tx, `SELECT `+certCols+` FROM signing_certificates WHERE kind='ca' AND status='active'
			AND (crl_next_update IS NULL OR crl_next_update <= $1) ORDER BY id LIMIT $2`, now, limit)
		return err
	})
	return out, mapErr(err)
}

// TenantsSystem implements repo.Store.
func (d *DB) TenantsSystem(ctx context.Context) (out []string, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id::text FROM signing_templates UNION SELECT tenant_id::text FROM signing_certificates ORDER BY 1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

// AppendAudit implements repo.Store.
func (d *DB) AppendAudit(ctx context.Context, row store.AuditRow) error {
	return mapErr(d.system(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signing_audit_events (id, tenant_id, at, actor_kind, actor_id, action, subject_kind, subject_id, outcome, reason, detail)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, row.ID, row.TenantID, orNow(row.At), row.ActorKind, row.ActorID, row.Action,
			row.SubjectKind, row.SubjectID, row.Outcome, row.Reason, js(row.Detail))
		return err
	}))
}
