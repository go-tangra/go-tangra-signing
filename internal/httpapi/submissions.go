package httpapi

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/signing"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

// ---- views (no e-mail addresses, object keys, values or IPs)

type signerView struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Name          string     `json:"name"`
	Party         string     `json:"party"`
	Position      int        `json:"position"`
	Status        string     `json:"status"`
	Method        string     `json:"method,omitempty"`
	CertSubject   string     `json:"cert_subject,omitempty"`
	CertSerial    string     `json:"cert_serial,omitempty"`
	CertIssuer    string     `json:"cert_issuer,omitempty"`
	DeclineReason string     `json:"decline_reason,omitempty"`
	InvitedAt     *time.Time `json:"invited_at"`
	OpenedAt      *time.Time `json:"opened_at"`
	SignedAt      *time.Time `json:"signed_at"`
	DeclinedAt    *time.Time `json:"declined_at"`
	RemindersSent int        `json:"reminders_sent"`
	MailError     string     `json:"mail_error,omitempty"`
}

func viewSigner(s store.Signer) signerView {
	return signerView{ID: s.ID, UserID: s.UserID, Name: s.Name, Party: s.Party, Position: s.Position, Status: s.Status,
		Method: s.Method, CertSubject: s.CertSubject, CertSerial: s.CertSerial, CertIssuer: s.CertIssuer, DeclineReason: s.DeclineReason,
		InvitedAt: s.InvitedAt, OpenedAt: s.OpenedAt, SignedAt: s.SignedAt, DeclinedAt: s.DeclinedAt, RemindersSent: s.RemindersSent,
		MailError: s.MailError}
}

type submissionView struct {
	ID             string          `json:"id"`
	TemplateID     string          `json:"template_id"`
	Name           string          `json:"name"`
	Mode           string          `json:"mode"`
	Status         string          `json:"status"`
	ExpiresAt      *time.Time      `json:"expires_at"`
	Reminder       *store.Reminder `json:"reminder"`
	CurrentVersion int             `json:"current_version"`
	FinalVersion   *int            `json:"final_version"`
	AuditTrail     bool            `json:"audit_trail"`
	CancelReason   string          `json:"cancel_reason,omitempty"`
	SentAt         *time.Time      `json:"sent_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	CreatedAt      time.Time       `json:"created_at"`
	CreatedBy      string          `json:"created_by"`
	Source         string          `json:"source"`
	Signers        []signerView    `json:"signers"`
	CanControl     bool            `json:"can_control"`
}

func viewSubmission(d submissions.Detail) submissionView {
	s := d.Submission
	signers := make([]signerView, 0, len(d.Signers))
	for _, sg := range d.Signers {
		signers = append(signers, viewSigner(sg))
	}
	return submissionView{ID: s.ID, TemplateID: s.TemplateID, Name: s.Name, Mode: s.Mode, Status: s.Status, ExpiresAt: s.ExpiresAt,
		Reminder: s.Reminder, CurrentVersion: s.CurrentVersion, FinalVersion: s.FinalVersion, AuditTrail: s.AuditTrailKey != "",
		CancelReason: s.CancelReason, SentAt: s.SentAt, CompletedAt: s.CompletedAt, CreatedAt: s.CreatedAt, CreatedBy: s.CreatedBy,
		Source: s.Source, Signers: signers, CanControl: d.CanControl}
}

type eventView struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	SignerID    *string        `json:"signer_id"`
	ActorUserID *string        `json:"actor_user_id"`
	Meta        map[string]any `json:"meta"`
	At          time.Time      `json:"at"`
}

type inboxView struct {
	SignerID         string     `json:"signer_id"`
	SubmissionID     string     `json:"submission_id"`
	SubmissionName   string     `json:"submission_name"`
	Party            string     `json:"party"`
	Status           string     `json:"status"`
	SubmissionStatus string     `json:"submission_status"`
	Sender           string     `json:"sender"`
	ExpiresAt        *time.Time `json:"expires_at"`
	SignedAt         *time.Time `json:"signed_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// fileName builds a download name from the submission name.
func fileName(name, suffix string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "document"
	}
	return name + suffix
}

type createBody struct {
	TemplateID string `json:"template_id"`
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	Signers    []struct {
		UserID   string `json:"user_id"`
		Party    string `json:"party"`
		Position *int   `json:"position"`
	} `json:"signers"`
	Prefill   map[string]string `json:"prefill"`
	ExpiresAt *time.Time        `json:"expires_at"`
	Reminder  *store.Reminder   `json:"reminder"`
}

func (s *Server) registerSubmissions(svc *submissions.Service) {
	s.withSubject("GET", Prefix+"/submissions", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		q := r.URL.Query()
		f := repo.SubmissionFilter{Status: q.Get("status"), TemplateID: q.Get("template_id"), Query: q.Get("q"),
			Page: queryInt(r, "page"), PageSize: queryInt(r, "page_size")}
		items, total, err := svc.List(r.Context(), subj, f, queryBool(r, "mine"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]submissionView, 0, len(items))
		for _, d := range items {
			out = append(out, viewSubmission(d))
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
	})
	s.withSubject("POST", Prefix+"/submissions", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b createBody
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		in := submissions.CreateInput{TemplateID: b.TemplateID, Name: b.Name, Mode: b.Mode, Prefill: b.Prefill, ExpiresAt: b.ExpiresAt, Reminder: b.Reminder}
		for _, sg := range b.Signers {
			in.Signers = append(in.Signers, submissions.SignerInput{UserID: sg.UserID, Party: sg.Party, Position: sg.Position})
		}
		d, err := svc.Create(r.Context(), subj, in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewSubmission(d))
	})
	s.withSubject("GET", Prefix+"/submissions/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		d, err := svc.Get(r.Context(), subj, r.PathValue("id"))
		s.reply(w, r, d, err)
	})
	s.withSubject("DELETE", Prefix+"/submissions/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		if err := svc.Delete(r.Context(), subj, r.PathValue("id")); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.withSubject("POST", Prefix+"/submissions/{id}/send", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		d, err := svc.Send(r.Context(), subj, r.PathValue("id"))
		s.reply(w, r, d, err)
	})
	s.withSubject("POST", Prefix+"/submissions/{id}/cancel", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			Reason string `json:"reason"`
		}
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		d, err := svc.Cancel(r.Context(), subj, r.PathValue("id"), b.Reason)
		s.reply(w, r, d, err)
	})
	s.withSubject("PUT", Prefix+"/submissions/{id}/signers/{sid}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			UserID string `json:"user_id"`
		}
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		d, err := svc.ReplaceSigner(r.Context(), subj, r.PathValue("id"), r.PathValue("sid"), b.UserID)
		s.reply(w, r, d, err)
	})
	s.withSubject("POST", Prefix+"/submissions/{id}/signers/{sid}/resend", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		d, err := svc.Resend(r.Context(), subj, r.PathValue("id"), r.PathValue("sid"))
		s.reply(w, r, d, err)
	})
	s.withSubject("GET", Prefix+"/submissions/{id}/events", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		evs, err := svc.Events(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]eventView, 0, len(evs))
		for _, e := range evs {
			meta := e.Meta
			if meta == nil {
				meta = map[string]any{}
			}
			out = append(out, eventView{ID: e.ID, Type: e.Type, SignerID: e.SignerID, ActorUserID: e.ActorUserID, Meta: meta, At: e.At})
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out})
	})
	s.withSubject("GET", Prefix+"/submissions/{id}/document", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		version := -1
		if r.URL.Query().Has("version") {
			version = queryInt(r, "version")
		}
		rc, v, sub, err := svc.Document(r.Context(), subj, r.PathValue("id"), version)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = rc.Close() }()
		suffix := ".pdf"
		if sub.FinalVersion == nil || v.Version != *sub.FinalVersion {
			suffix = fmt.Sprintf(" (v%d).pdf", v.Version)
		}
		_ = Download(w, "application/pdf", fileName(sub.Name, suffix), v.Size, rc)
	})
	s.withSubject("GET", Prefix+"/submissions/{id}/audit-trail", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		rc, sub, err := svc.AuditTrail(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = rc.Close() }()
		_ = Download(w, "application/pdf", fileName(sub.Name, " - audit trail.pdf"), 0, rc)
	})
	s.withSubject("GET", Prefix+"/submissions/{id}/package", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		id := r.PathValue("id")
		doc, v, sub, err := svc.Document(r.Context(), subj, id, -1)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = doc.Close() }()
		if sub.FinalVersion == nil || v.Version != *sub.FinalVersion {
			s.fail(w, r, apperr.NotFound) // only completed submissions have a package
			return
		}
		trail, _, err := svc.AuditTrail(r.Context(), subj, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = trail.Close() }()
		h := w.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Disposition", disposition(fileName(sub.Name, ".zip")))
		w.WriteHeader(http.StatusOK)
		zw := zip.NewWriter(w)
		for _, f := range []struct {
			name string
			body io.Reader
		}{{fileName(sub.Name, ".pdf"), doc}, {fileName(sub.Name, " - audit trail.pdf"), trail}} {
			fw, err := zw.CreateHeader(&zip.FileHeader{Name: zipName(f.name), Method: zip.Deflate, Modified: s.now()})
			if err != nil {
				return
			}
			if _, err := io.Copy(fw, f.body); err != nil {
				return
			}
		}
		_ = zw.Close()
	})
	s.withSubject("GET", Prefix+"/inbox", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		items, total, err := svc.Inbox(r.Context(), subj, r.URL.Query().Get("state") == "signed", queryInt(r, "page"), queryInt(r, "page_size"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]inboxView, 0, len(items))
		for _, it := range items {
			out = append(out, inboxView{SignerID: it.Signer.ID, SubmissionID: it.Submission.ID, SubmissionName: it.Submission.Name,
				Party: it.Signer.Party, Status: it.Signer.Status, SubmissionStatus: it.Submission.Status, Sender: it.Submission.CreatedBy,
				ExpiresAt: it.Submission.ExpiresAt, SignedAt: it.Signer.SignedAt, CreatedAt: it.Submission.CreatedAt})
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
	})
	s.withSubject("GET", Prefix+"/users", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		members, err := svc.Members(r.Context(), subj, r.URL.Query().Get("q"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]map[string]string, 0, len(members))
		for _, m := range members {
			out = append(out, map[string]string{"user_id": m.UserID, "display_name": m.DisplayName})
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out})
	})
}

// dataURL decodes a "data:image/png;base64,…" (or JPEG) signature image;
// "" is no image.
func dataURL(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	head, data, ok := strings.Cut(v, ",")
	if !ok || (head != "data:image/png;base64" && head != "data:image/jpeg;base64") {
		return nil, apperr.Validation.WithField("signature_image")
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, apperr.Validation.WithField("signature_image")
	}
	return b, nil
}

// zipName keeps an entry name inside the archive (no paths).
func zipName(n string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(n)
}

func (s *Server) now() time.Time { return time.Now().UTC() }

func (s *Server) reply(w http.ResponseWriter, r *http.Request, d submissions.Detail, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, viewSubmission(d))
}

// ---- signing

type certStateView struct {
	State       string     `json:"state"`
	LockedUntil *time.Time `json:"locked_until"`
}

type sessionView struct {
	SignerID        string            `json:"signer_id"`
	SubmissionID    string            `json:"submission_id"`
	SubmissionName  string            `json:"submission_name"`
	Party           string            `json:"party"`
	Status          string            `json:"status"`
	Fields          []store.Field     `json:"fields"`
	AllFields       []store.Field     `json:"all_fields"`
	Values          map[string]string `json:"values"`
	DocumentVersion int               `json:"document_version"`
	Certificate     certStateView     `json:"certificate"`
	CanSign         bool              `json:"can_sign"`
	Reason          string            `json:"reason,omitempty"`
}

// maxUploads bounds the parts of a signing request.
const maxUploads = 60

func (s *Server) registerSigning(svc *signing.Service, maxUpload int64, maxImage int64) {
	s.withSubject("GET", Prefix+"/signing/{signer_id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		ss, err := svc.Session(r.Context(), subj, r.PathValue("signer_id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		fields, all := ss.Fields, ss.AllFields
		if fields == nil {
			fields = []store.Field{}
		}
		if all == nil {
			all = []store.Field{}
		}
		WriteJSON(w, http.StatusOK, sessionView{SignerID: ss.Signer.ID, SubmissionID: ss.Submission.ID, SubmissionName: ss.Submission.Name,
			Party: ss.Signer.Party, Status: ss.Signer.Status, Fields: fields, AllFields: all, Values: ss.Values,
			DocumentVersion: ss.Submission.CurrentVersion, Certificate: certStateView{State: ss.CertState, LockedUntil: ss.LockedTill},
			CanSign: ss.CanSign, Reason: ss.Reason})
	})
	s.withSubject("GET", Prefix+"/signing/{signer_id}/document", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		rc, v, sub, err := svc.Document(r.Context(), subj, r.PathValue("signer_id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = rc.Close() }()
		_ = Download(w, "application/pdf", fileName(sub.Name, ".pdf"), v.Size, rc)
	})
	s.withSubject("POST", Prefix+"/signing/{signer_id}/open", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		if err := svc.Open(r.Context(), subj, r.PathValue("signer_id")); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.withSubject("POST", Prefix+"/signing/{signer_id}/sign", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		mp, err := ReadMultipart(r, maxUploads, func(name string) int64 {
			switch {
			case name == "signature":
				return maxImage
			case strings.HasPrefix(name, "upload."):
				return maxUpload
			}
			return 0
		})
		if err != nil {
			if errors.Is(err, ErrPartTooLarge) {
				err = apperr.PayloadTooLarge.WithField(strings.TrimPrefix(strings.TrimPrefix(err.Error(), ErrPartTooLarge.Error()+": "), "upload."))
			}
			s.fail(w, r, err)
			return
		}
		in := signing.Input{Values: map[string]string{}, Uploads: map[string][]byte{}, PIN: mp.Values["pin"],
			IP: ClientAddr(r), UserAgent: UserAgent(r)}
		for k := range mp.Values {
			if k != "values" && k != "pin" {
				s.fail(w, r, ErrMalformed)
				return
			}
		}
		if raw := mp.Values["values"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &in.Values); err != nil || len(in.Values) > 500 {
				s.fail(w, r, apperr.Validation.WithField("values"))
				return
			}
		}
		for name, p := range mp.Files {
			if name == "signature" {
				in.Signature = p.Data
				continue
			}
			in.Uploads[strings.TrimPrefix(name, "upload.")] = p.Data
		}
		res, err := svc.Sign(r.Context(), subj, r.PathValue("signer_id"), in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": res.SignerStatus, "submission_status": res.SubmissionStatus})
	})
	s.withSubject("POST", Prefix+"/signing/{signer_id}/qes/prepare", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			Values         map[string]string `json:"values"`
			Chain          []string          `json:"chain"`
			SignatureImage string            `json:"signature_image"`
		}
		if err := DecodeJSON(r, &b, 2<<20); err != nil {
			s.fail(w, r, err)
			return
		}
		img, err := dataURL(b.SignatureImage)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p, err := svc.PrepareQES(r.Context(), subj, r.PathValue("signer_id"), signing.QESInput{Values: b.Values, Chain: b.Chain, Signature: img})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := map[string]any{"preparation_id": p.ID, "digest_b64": base64.StdEncoding.EncodeToString(p.Digest),
			"signed_attrs_b64": base64.StdEncoding.EncodeToString(p.SignedAttrs), "hash_algorithm": "SHA256",
			"expires_at": p.ExpiresAt.UTC().Format(time.RFC3339)}
		if p.OriginProof != nil {
			out["signed_contents_b64"] = base64.StdEncoding.EncodeToString(p.OriginProof)
			out["signed_contents_cert_b64"] = base64.StdEncoding.EncodeToString(p.OriginCertificate)
		}
		WriteJSON(w, http.StatusOK, out)
	})
	s.withSubject("POST", Prefix+"/signing/{signer_id}/qes/complete", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			PreparationID string `json:"preparation_id"`
			SignatureB64  string `json:"signature_b64"`
		}
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		sig, err := base64.StdEncoding.DecodeString(b.SignatureB64)
		if err != nil {
			s.fail(w, r, apperr.Validation.WithField("signature_b64"))
			return
		}
		res, err := svc.CompleteQES(r.Context(), subj, r.PathValue("signer_id"), b.PreparationID, sig, ClientAddr(r), UserAgent(r))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": res.SignerStatus, "submission_status": res.SubmissionStatus})
	})
	s.withSubject("POST", Prefix+"/signing/{signer_id}/decline", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			Reason string `json:"reason"`
		}
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		res, err := svc.Decline(r.Context(), subj, r.PathValue("signer_id"), b.Reason, ClientAddr(r))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": res.SignerStatus, "submission_status": res.SubmissionStatus})
	})
}
