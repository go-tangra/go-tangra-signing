package httpapi

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/documents"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/warden"
)

func (s *Server) registerAdmin(svc *certs.Admin) {
	s.withSubject("GET", Prefix+"/certificates", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		req, ok := parseList(w, r, store.CertificateList)
		if !ok {
			return
		}
		q := r.URL.Query()
		items, total, err := svc.List(r.Context(), subj, repo.CertificateFilter{Kind: q.Get("kind"), Status: q.Get("status"),
			Query: q.Get("q"), Page: req.Page, PageSize: req.PageSize, Sort: req.Sort, Order: req.Order})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]certificateView, 0, len(items))
		for _, c := range items {
			out = append(out, viewCertificate(c, false))
		}
		WriteJSON(w, http.StatusOK, listPage(out, total, req))
	})
	s.withSubject("POST", Prefix+"/certificates", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			SubjectCN     string `json:"subject_cn"`
			Email         string `json:"email"`
			ValidityYears int    `json:"validity_years"`
		}
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		c, err := svc.CreateAdmin(r.Context(), subj, b.SubjectCN, b.Email, b.ValidityYears)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewCertificate(c, true))
	})
	s.withSubject("GET", Prefix+"/certificates/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		c, err := svc.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewCertificate(c, true))
	})
	s.withSubject("POST", Prefix+"/certificates/{id}/revoke", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var b struct {
			Reason string `json:"reason"`
		}
		if err := DecodeJSON(r, &b, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		c, err := svc.Revoke(r.Context(), subj, r.PathValue("id"), b.Reason)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewCertificate(c, false))
	})
	s.withSubject("GET", Prefix+"/ca/crl", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		der, err := svc.CRL(r.Context(), subj)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		_ = Download(w, "application/pkix-crl", "tenant-ca.crl", int64(len(der)), bytes.NewReader(der))
	})
}

// source reads the document source of a multipart request: a "file" part
// or submission_id (+ version).
func source(mp *Multipart) (documents.Source, error) {
	src := documents.Source{Version: -1}
	if f, ok := mp.Files["file"]; ok {
		src.PDF = f.Data
	}
	if id := mp.Values["submission_id"]; id != "" {
		if src.PDF != nil {
			return src, apperr.Validation.WithField("submission_id")
		}
		src.SubmissionID = id
		if v := mp.Values["version"]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 1000 {
				return src, apperr.Validation.WithField("version")
			}
			src.Version = n
		}
	}
	return src, nil
}

func onlyParts(mp *Multipart, allowed ...string) error {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for k := range mp.Values {
		if !ok[k] {
			return ErrMalformed
		}
	}
	for k := range mp.Files {
		if k != "file" {
			return ErrMalformed
		}
	}
	return nil
}

func (s *Server) registerDocuments(svc *documents.Service, maxPDF int64) {
	read := func(r *http.Request) (*Multipart, error) {
		mp, err := ReadMultipart(r, 16, func(name string) int64 {
			if name == "file" {
				return maxPDF
			}
			return 0
		})
		if err != nil && strings.HasPrefix(err.Error(), ErrPartTooLarge.Error()) {
			return nil, apperr.PayloadTooLarge.WithField("file")
		}
		return mp, err
	}
	s.withSubject("POST", Prefix+"/documents/sign", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		mp, err := read(r)
		if err == nil {
			err = onlyParts(mp, "submission_id", "version", "certificate_id", "reason", "location", "contact", "tsa_url", "tsa_secret_ref")
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		src, err := source(mp)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v := mp.Values
		// The TSA secret is read from Warden on behalf of the signed-in user.
		ctx := warden.WithUserToken(r.Context(), authclient.BearerToken(r.Header.Get("Authorization")))
		id, exp, err := svc.Sign(ctx, subj, documents.SignInput{Source: src, CertificateID: v["certificate_id"], Reason: v["reason"],
			Location: v["location"], Contact: v["contact"], TSAURL: v["tsa_url"], TSASecretRef: v["tsa_secret_ref"]})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{"id": id, "expires_at": exp.UTC().Format(time.RFC3339)})
	})
	s.withSubject("GET", Prefix+"/documents/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		rc, err := svc.Download(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = rc.Close() }()
		_ = Download(w, "application/pdf", "signed.pdf", 0, rc)
	})
	s.withSubject("POST", Prefix+"/verify", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		mp, err := read(r)
		if err == nil {
			err = onlyParts(mp, "submission_id", "version")
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		src, err := source(mp)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		res, err := svc.Verify(r.Context(), subj, src)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]map[string]any, 0, len(res.Signatures))
		for _, sg := range res.Signatures {
			out = append(out, map[string]any{"signer": sg.Signer, "time": sg.Time, "reason": sg.Reason, "location": sg.Location,
				"integrity": sg.Integrity, "trust": sg.Trust, "revocation": sg.Revocation, "method": sg.Method, "issuer": sg.Issuer,
				"serial": sg.Serial})
		}
		WriteJSON(w, http.StatusOK, map[string]any{"signatures": out, "modified_after_last_signature": res.ModifiedAfterLast})
	})
}
