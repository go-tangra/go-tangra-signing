package httpapi

import (
	"encoding/pem"
	"net/http"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// certificateView never carries key material.
type certificateView struct {
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	IssuerID          *string    `json:"issuer_id"`
	OwnerUserID       *string    `json:"owner_user_id"`
	SubjectCN         string     `json:"subject_cn"`
	Email             string     `json:"email"`
	Serial            string     `json:"serial"`
	FingerprintSHA256 string     `json:"fingerprint_sha256"`
	NotBefore         time.Time  `json:"not_before"`
	NotAfter          time.Time  `json:"not_after"`
	Status            string     `json:"status"`
	RevokedAt         *time.Time `json:"revoked_at"`
	RevocationReason  string     `json:"revocation_reason"`
	LockedUntil       *time.Time `json:"locked_until"`
	IssuerCN          string     `json:"issuer_cn,omitempty"`
	PEM               string     `json:"pem,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

func viewCertificate(c store.Certificate, withPEM bool) certificateView {
	v := certificateView{ID: c.ID, Kind: c.Kind, IssuerID: c.IssuerID, OwnerUserID: c.OwnerUserID, SubjectCN: c.SubjectCN, Email: c.Email,
		Serial: c.Serial, FingerprintSHA256: pki.Fingerprint(c), NotBefore: c.NotBefore, NotAfter: c.NotAfter, Status: c.Status,
		RevokedAt: c.RevokedAt, RevocationReason: c.RevocationReason, LockedUntil: c.LockedUntil, CreatedAt: c.CreatedAt}
	if x, err := pki.Parse(c); err == nil {
		v.IssuerCN = x.Issuer.CommonName
	}
	if withPEM {
		v.PEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.CertDER}))
	}
	return v
}

type signedView struct {
	SubmissionID   string    `json:"submission_id"`
	SubmissionName string    `json:"submission_name"`
	SignedAt       time.Time `json:"signed_at"`
}

func viewMine(m certs.MyCertificate) map[string]any {
	out := map[string]any{"certificate": nil, "signed": []signedView{}}
	if m.Certificate != nil {
		out["certificate"] = viewCertificate(*m.Certificate, true)
	}
	signed := make([]signedView, 0, len(m.Signed))
	for _, s := range m.Signed {
		signed = append(signed, signedView{SubmissionID: s.SubmissionID, SubmissionName: s.SubmissionName, SignedAt: s.SignedAt})
	}
	out["signed"] = signed
	return out
}

func (s *Server) registerMe(me *certs.Me) {
	s.withSubject("GET", Prefix+"/me/certificate", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		m, err := me.Get(r.Context(), subj)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewMine(m))
	})
	pinBody := func(r *http.Request) (string, error) {
		var body struct {
			PIN string `json:"pin"`
		}
		err := DecodeJSON(r, &body, 0)
		return body.PIN, err
	}
	s.withSubject("POST", Prefix+"/me/certificate", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		pin, err := pinBody(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		m, err := me.Setup(r.Context(), subj, pin)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewMine(m))
	})
	s.withSubject("POST", Prefix+"/me/certificate/pin", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var body struct {
			Old string `json:"old_pin"`
			New string `json:"new_pin"`
		}
		if err := DecodeJSON(r, &body, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := me.ChangePIN(r.Context(), subj, body.Old, body.New); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.withSubject("POST", Prefix+"/me/certificate/renew", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		pin, err := pinBody(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		m, err := me.Renew(r.Context(), subj, pin)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewMine(m))
	})
	s.withSubject("POST", Prefix+"/me/certificate/revoke", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		if err := me.Revoke(r.Context(), subj); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
