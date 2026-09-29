package signing

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"io"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
	"github.com/go-tangra/go-tangra-signing/v4/internal/qes"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Origin signs BISS's "signedContents" (the configured origin certificate).
type Origin struct {
	Cert   *x509.Certificate
	Signer crypto.Signer
}

// QESInput prepares a qualified signature.
type QESInput struct {
	Values    map[string]string
	Chain     []string // base64 DER, leaf first (from BISS getsigner)
	Signature []byte   // drawn/typed signature image for the appearance (optional)
}

// QESPrepared is what the browser hands to BISS.
type QESPrepared struct {
	ID                string
	Digest            []byte
	SignedAttrs       []byte // BISS "contents" (contentType data, SHA256)
	ExpiresAt         time.Time
	OriginProof       []byte // BISS "signedContents" (nil without an origin)
	OriginCertificate []byte
}

func (s *Service) qesTTL() time.Duration {
	if s.d.QESTTL > 0 {
		return s.d.QESTTL
	}
	return 10 * time.Minute
}

// PrepareQES fixes the document with the signer's values and a reserved
// signature for the card's certificate (FR-022). The state lives in the
// shared store for the TTL, so any instance can complete it.
func (s *Service) PrepareQES(ctx context.Context, subj authz.Subjects, signerID string, in QESInput) (QESPrepared, error) {
	p, err := s.prepareQES(ctx, subj, signerID, in)
	if err != nil {
		s.d.Metrics.QES("prepare", reasonOf(err))
		s.record(ctx, subj, audit.QESPrepare, signerID, audit.OutcomeRefused, reasonOf(err), nil)
		return p, err
	}
	s.d.Metrics.QES("prepare", "ok")
	s.record(ctx, subj, audit.QESPrepare, signerID, audit.OutcomeOK, "", nil)
	return p, err
}

func (s *Service) prepareQES(ctx context.Context, subj authz.Subjects, signerID string, in QESInput) (QESPrepared, error) {
	now := s.d.Now()
	if s.d.Limited != nil {
		if limited, err := s.d.Limited(ctx, subj.TenantID, subj.UserID); err == nil && limited {
			return QESPrepared{}, apperr.RateLimited
		}
	}
	chain, err := qes.ParseChain(in.Chain)
	if err != nil {
		return QESPrepared{}, apperr.Validation.WithField("chain")
	}
	if err := qes.CheckLeaf(chain[0], now); err != nil {
		return QESPrepared{}, apperr.CertificateUnusable.WithField("chain")
	}
	sub, all, sg, err := s.own(ctx, s.d.Store, subj, signerID, false)
	if err != nil {
		return QESPrepared{}, err
	}
	if err := refusal(sub, all, sg, now); err != nil {
		return QESPrepared{}, err
	}
	p, err := s.check(sub, all, sg, Input{Values: in.Values, Signature: in.Signature})
	if err != nil {
		return QESPrepared{}, err
	}
	doc, err := s.current(ctx, s.d.Store, sub)
	if err != nil {
		return QESPrepared{}, err
	}
	stamped, appearance, err := Stamped(doc, p.fields, p.values, nil, in.Signature, sg.Name, chain[0].Issuer.CommonName, now)
	if err != nil {
		return QESPrepared{}, pdfErr(err)
	}
	prep, err := sign.Prepare(stamped, chain, sign.Options{Name: sg.Name, Reason: "Signed by " + sg.Name, Location: s.d.Location,
		Time: now, Visible: appearance})
	if err != nil {
		return QESPrepared{}, pdfErr(err)
	}
	id := store.NewID()
	key := blob.QESPrepared(sub.TenantID, id)
	if _, err := s.d.Blob.Put(ctx, key, bytes.NewReader(prep.PDF), int64(len(prep.PDF)), "application/pdf"); err != nil {
		return QESPrepared{}, err
	}
	q := store.QESPreparation{ID: id, TenantID: sub.TenantID, SignerID: sg.ID, ChainDER: qes.ChainDER(chain), SignedAttrs: prep.SignedAttrs,
		Digest: prep.Digest, PreparedKey: key, BasedOnVersion: sub.CurrentVersion, Values: p.values, ExpiresAt: now.Add(s.qesTTL()), CreatedAt: now}
	if err := s.d.Store.CreateQES(ctx, q); err != nil {
		_ = s.d.Blob.Delete(context.WithoutCancel(ctx), key)
		return QESPrepared{}, err
	}
	out := QESPrepared{ID: id, Digest: prep.Digest, SignedAttrs: prep.SignedAttrs, ExpiresAt: q.ExpiresAt}
	if o := s.d.Origin; o != nil && o.Signer != nil && o.Cert != nil {
		if out.OriginProof, err = qes.OriginProof(o.Signer, prep.SignedAttrs); err != nil {
			return QESPrepared{}, err
		}
		out.OriginCertificate = o.Cert.Raw
	}
	return out, nil
}

// CompleteQES embeds the card's signature (FR-023): it is verified over the
// prepared digest with the leaf of the chain stored at preparation, the leaf
// must still be valid, the document must not have moved on, and the result
// must verify before it becomes version n+1.
func (s *Service) CompleteQES(ctx context.Context, subj authz.Subjects, signerID, preparationID string, signature []byte, ip, ua string) (Result, error) {
	res, err := s.completeQES(ctx, subj, signerID, preparationID, signature, ip, ua)
	outcome := "ok"
	if err != nil {
		outcome = reasonOf(err)
		s.record(ctx, subj, audit.SignerSign, signerID, audit.OutcomeRefused, outcome, map[string]any{"method": store.MethodQES})
	}
	s.d.Metrics.QES("complete", outcome)
	s.d.Metrics.Signing(store.MethodQES, outcome)
	return res, err
}

func (s *Service) completeQES(ctx context.Context, subj authz.Subjects, signerID, preparationID string, signature []byte, ip, ua string) (Result, error) {
	now := s.d.Now()
	q, err := s.d.Store.GetQES(ctx, subj.TenantID, preparationID)
	if errors.Is(err, repo.ErrNotFound) {
		return Result{}, apperr.PreparationExpired
	} else if err != nil {
		return Result{}, err
	}
	if q.SignerID != signerID {
		return Result{}, apperr.PreparationExpired
	}
	if q.UsedAt != nil || !now.Before(q.ExpiresAt) {
		return Result{}, apperr.PreparationExpired
	}
	chain, err := qes.ParseDER(q.ChainDER)
	if err != nil {
		return Result{}, apperr.PreparationExpired
	}
	if err := qes.CheckLeaf(chain[0], now); err != nil {
		return Result{}, apperr.CertificateUnusable.WithField("chain")
	}
	sig, err := qes.Normalize(signature, chain[0].PublicKey)
	if err != nil {
		return Result{}, apperr.QESSignatureInvalid
	}
	if err := qes.Verify(chain[0].PublicKey, q.Digest, sig); err != nil {
		return Result{}, apperr.QESSignatureInvalid
	}
	prepared, err := s.read(ctx, q.PreparedKey)
	if err != nil {
		return Result{}, apperr.PreparationExpired
	}
	pdf, err := sign.Complete(prepared, chain[0], sig)
	if err != nil {
		return Result{}, apperr.QESSignatureInvalid
	}
	if sigs, err := verify.Verify(pdf, verify.Options{Now: now}); err != nil || len(sigs) == 0 || !sigs[len(sigs)-1].Intact {
		return Result{}, apperr.QESSignatureInvalid
	}

	var c committed
	err = s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		sub, all, sg, err := s.own(ctx, tx, subj, signerID, true)
		if err != nil {
			return err
		}
		if err := refusal(sub, all, sg, now); err != nil {
			return err
		}
		if sub.CurrentVersion != q.BasedOnVersion {
			return apperr.DocumentChanged
		}
		if err := tx.MarkQESUsed(ctx, subj.TenantID, q.ID, now); err != nil {
			return err
		}
		c, err = s.commit(ctx, tx, subj, sub, all, sg, finished{pdf: pdf, values: q.Values, method: store.MethodQES,
			subject: chain[0].Subject.CommonName, serial: chain[0].SerialNumber.Text(16), issuer: chain[0].Issuer.CommonName,
			ip: ip, ua: ua}, now)
		return err
	})
	if err != nil {
		s.cleanup(ctx, c.objects)
		return Result{}, err
	}
	_ = s.d.Blob.Delete(context.WithoutCancel(ctx), q.PreparedKey)
	return s.after(ctx, subj, c, store.MethodQES), nil
}

func (s *Service) read(ctx context.Context, key string) ([]byte, error) {
	rc, err := s.d.Blob.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, s.d.Limits.MaxPDFBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.d.Limits.MaxPDFBytes {
		return nil, apperr.PayloadTooLarge
	}
	return data, nil
}
