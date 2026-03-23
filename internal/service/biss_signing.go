package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/digitorus/pkcs7"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submitter"
	pdfsign "github.com/go-tangra/go-tangra-signing/pkg/pdf/sign"
	appViewer "github.com/go-tangra/go-tangra-signing/pkg/viewer"
)

// bissSession stores the prepared PDF between PrepareForBissSigning and CompleteBissSigning.
type bissSession struct {
	preparedPDF    []byte
	pdfHash        []byte   // SHA-256 hash of the PDF byte ranges
	signedAttrsDER []byte   // DER-encoded signedAttrs (what BISS actually signs)
	signContent    []byte   // PDF byte range content for PKCS#7
	submitterID    string
	submissionID   string
	tenantID       uint32
	createdAt      time.Time
}

var (
	bissSessions   = make(map[string]*bissSession)
	bissSessionsMu sync.Mutex
)

func storeBissSession(id string, session *bissSession) {
	bissSessionsMu.Lock()
	defer bissSessionsMu.Unlock()
	bissSessions[id] = session
	// Clean expired sessions (older than 10 minutes)
	for k, v := range bissSessions {
		if time.Since(v.createdAt) > 10*time.Minute {
			delete(bissSessions, k)
		}
	}
}

func loadBissSession(id string) *bissSession {
	bissSessionsMu.Lock()
	defer bissSessionsMu.Unlock()
	s, ok := bissSessions[id]
	if !ok || time.Since(s.createdAt) > 10*time.Minute {
		delete(bissSessions, id)
		return nil
	}
	return s
}

func deleteBissSession(id string) {
	bissSessionsMu.Lock()
	defer bissSessionsMu.Unlock()
	delete(bissSessions, id)
}

// loadServerCert loads the server SSL certificate and private key for BISS signedContents.
// Uses BISS_CERT_PATH / BISS_KEY_PATH env vars, falls back to CERTS_DIR/biss, then CERTS_DIR/signing-server.
func loadServerCert() (*x509.Certificate, crypto.Signer, error) {
	certPath := os.Getenv("BISS_CERT_PATH")
	keyPath := os.Getenv("BISS_KEY_PATH")

	if certPath == "" || keyPath == "" {
		certsDir := os.Getenv("CERTS_DIR")
		if certsDir == "" {
			certsDir = "/app/certs"
		}
		// Try BISS-specific cert first (e.g., production SSL cert)
		bissCert := certsDir + "/biss/cert.pem"
		bissKey := certsDir + "/biss/key.pem"
		if _, err := os.Stat(bissCert); err == nil {
			certPath = bissCert
			keyPath = bissKey
		} else {
			certPath = certsDir + "/signing-server/server.crt"
			keyPath = certsDir + "/signing-server/server.key"
		}
	}

	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load server cert: %w", err)
	}

	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("parse server cert: %w", err)
	}

	signer, ok := tlsCert.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("private key does not implement crypto.Signer")
	}

	return x509Cert, signer, nil
}

// PrepareForBissSigning creates a PDF with field overlays and a PAdES signature placeholder,
// computes the hash, signs it with the server cert, and returns everything needed for BISS.
func (s *SessionService) PrepareForBissSigning(ctx context.Context, req *signingV1.PrepareForBissSigningRequest) (*signingV1.PrepareForBissSigningResponse, error) {
	ctx = appViewer.NewSystemViewerContext(ctx)

	if req.Token == "" {
		return nil, signingV1.ErrorBadRequest("token is required")
	}

	// Look up submitter
	sub, err := s.submitterRepo.GetBySlug(ctx, req.Token)
	if err != nil || sub == nil {
		return nil, signingV1.ErrorSubmitterNotFound("signing session not found")
	}
	if sub.Status == submitter.StatusSUBMITTER_STATUS_COMPLETED {
		return nil, signingV1.ErrorSubmitterAlreadyCompleted("already completed")
	}

	// Get submission and template
	submission, err := s.submissionRepo.GetByID(ctx, sub.SubmissionID)
	if err != nil || submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	template, err := s.templateRepo.GetByID(ctx, submission.TemplateID)
	if err != nil || template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Download the current PDF (with previous signers' data if any)
	pdfKey := template.FileKey
	if submission.CurrentPdfKey != "" {
		pdfKey = submission.CurrentPdfKey
	}
	pdfContent, err := s.storage.Download(ctx, pdfKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	// Overlay this signer's field values onto the PDF
	values := fieldValuesToMap(req.FieldValues)
	fieldValues := buildFieldValuesForOverlay(submission.TemplateFieldsSnapshot, sub.SigningOrder, values)
	if len(fieldValues) > 0 {
		overlaidPDF, err := s.pdfGenerator.overlayFields(pdfContent, fieldValues)
		if err != nil {
			s.log.Warnf("failed to overlay fields for BISS signing: %v", err)
		} else {
			pdfContent = overlaidPDF
		}
	}

	// Prepare the PDF for external signing (create placeholder, compute hash)
	signData := pdfsign.SignData{
		Signature: pdfsign.SignDataSignature{
			CertType:   pdfsign.ApprovalSignature,
			DocMDPPerm: pdfsign.AllowFillingExistingFormFieldsAndSignaturesPerms,
			Info: pdfsign.SignDataSignatureInfo{
				Name:     sub.Name,
				Location: "GoTangra Signing",
				Reason:   "Document signing",
			},
		},
		DigestAlgorithm: crypto.SHA256,
	}

	preparedPDF, hashBytes, err := pdfsign.PrepareForExternalSigning(
		bytes.NewReader(pdfContent), int64(len(pdfContent)), signData,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare PDF for signing: %w", err)
	}

	// Sign the hash with the server cert (for BISS signedContents verification)
	serverCert, serverSigner, err := loadServerCert()
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	// Build the PKCS#7 signedAttrs that will be signed by BISS.
	// For PAdES, the signature must be over SHA256(DER_signedAttrs), not over the raw pdfHash.
	// So we send DER_signedAttrs to BISS with contentType "data", and BISS signs SHA256(DER_signedAttrs).

	// Extract byte range content from prepared PDF for PKCS#7
	signContent, err := extractByteRangeContent(preparedPDF)
	if err != nil {
		return nil, fmt.Errorf("failed to extract byte range content: %w", err)
	}

	// Build signedAttrs DER using pkcs7 library (create a temp SignedData to extract attrs)
	signedAttrsDER, err := buildSignedAttrsDER(signContent, hashBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to build signedAttrs: %w", err)
	}

	// The content sent to BISS = DER_signedAttrs (with contentType "data", BISS signs SHA256(DER_signedAttrs))
	bissContent := signedAttrsDER

	// Sign for BISS signedContents verification: SHA256(SHA256(content) || content)
	contentHash := sha256.Sum256(bissContent)
	combined := append(contentHash[:], bissContent...)
	finalDigest := sha256.Sum256(combined)
	signedHash, err := serverSigner.Sign(rand.Reader, finalDigest[:], crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("failed to sign hash with server cert: %w", err)
	}

	sessionID := generateUUID()
	tenantID := derefUint32(sub.TenantID)

	storeBissSession(sessionID, &bissSession{
		preparedPDF:    preparedPDF,
		pdfHash:        hashBytes,
		signedAttrsDER: signedAttrsDER,
		signContent:    signContent,
		submitterID:    sub.ID,
		submissionID:   submission.ID,
		tenantID:       tenantID,
		createdAt:      time.Now(),
	})

	serverCertB64 := base64.StdEncoding.EncodeToString(serverCert.Raw)

	return &signingV1.PrepareForBissSigningResponse{
		HashBase64:       base64.StdEncoding.EncodeToString(bissContent), // DER signedAttrs, not raw hash
		SignedHashBase64: base64.StdEncoding.EncodeToString(signedHash),
		ServerCertBase64: serverCertB64,
		SessionId:        sessionID,
	}, nil
}

// CompleteBissSigning receives the PKCS#7 signature from BISS and embeds it into the prepared PDF.
func (s *SessionService) CompleteBissSigning(ctx context.Context, req *signingV1.CompleteBissSigningRequest) (*signingV1.CompleteBissSigningResponse, error) {
	ctx = appViewer.NewSystemViewerContext(ctx)

	if req.Token == "" || req.SessionId == "" || req.Pkcs7SignatureBase64 == "" {
		return nil, signingV1.ErrorBadRequest("token, session_id, and pkcs7_signature_base64 are required")
	}

	// Load the prepared PDF from the temporary store
	session := loadBissSession(req.SessionId)
	if session == nil {
		return nil, signingV1.ErrorBadRequest("BISS session expired or not found")
	}
	deleteBissSession(req.SessionId)

	// Verify the submitter matches
	sub, err := s.submitterRepo.GetBySlug(ctx, req.Token)
	if err != nil || sub == nil {
		return nil, signingV1.ErrorSubmitterNotFound("signing session not found")
	}
	if sub.ID != session.submitterID {
		return nil, signingV1.ErrorBadRequest("token does not match BISS session")
	}

	// Decode the raw signature from BISS
	rawSignature, err := base64.StdEncoding.DecodeString(req.Pkcs7SignatureBase64)
	if err != nil {
		return nil, signingV1.ErrorBadRequest("invalid signature encoding")
	}

	// Parse the signer's certificate chain from BISS
	var signerCert *x509.Certificate
	var certChain []*x509.Certificate
	for _, certB64 := range req.CertificateChain {
		certBytes, err := base64.StdEncoding.DecodeString(certB64)
		if err != nil {
			continue
		}
		cert, err := x509.ParseCertificate(certBytes)
		if err != nil {
			continue
		}
		certChain = append(certChain, cert)
	}
	if len(certChain) > 0 {
		signerCert = certChain[0]
	}
	if signerCert == nil {
		return nil, signingV1.ErrorBadRequest("no valid signer certificate provided")
	}

	// Build PKCS#7/CMS container using stored signContent + BISS signature
	pkcs7Container, err := buildPKCS7Container(session.signContent, session.pdfHash, rawSignature, signerCert, certChain)
	if err != nil {
		return nil, fmt.Errorf("failed to build PKCS#7 container: %w", err)
	}

	// Embed the PKCS#7 into the prepared PDF
	signedPDF, err := pdfsign.EmbedExternalSignature(session.preparedPDF, pkcs7Container)
	if err != nil {
		return nil, fmt.Errorf("failed to embed signature: %w", err)
	}

	// Upload the signed PDF
	signedKey := fmt.Sprintf("%d/signed/%s/biss-signed.pdf", session.tenantID, session.submissionID)
	if _, uploadErr := s.storage.UploadRaw(ctx, signedKey, signedPDF, "application/pdf"); uploadErr != nil {
		return nil, fmt.Errorf("failed to upload signed PDF: %w", uploadErr)
	}

	// Mark submitter as completed
	now := time.Now()
	values := make(map[string]interface{})
	values["_biss_signed"] = true
	values["_signed_document_key"] = signedKey
	if _, err := s.submitterRepo.Complete(ctx, sub.ID, values, "", "", now); err != nil {
		return nil, err
	}

	// Update submission with signed document key
	if err := s.submissionRepo.UpdateSignedDocumentKey(ctx, session.submissionID, signedKey); err != nil {
		s.log.Errorf("failed to update signed document key: %v", err)
	}

	// Also update current_pdf_key for next signer
	if err := s.submissionRepo.UpdateCurrentPdfKey(ctx, session.submissionID, signedKey); err != nil {
		s.log.Errorf("failed to update current_pdf_key: %v", err)
	}

	tenantID := session.tenantID

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submitter.biss_signed", "",
		"submitter", sub.ID, map[string]interface{}{"signed_key": signedKey}, "")

	// Handle sequential workflow
	submission, _ := s.submissionRepo.GetByID(ctx, session.submissionID)
	if submission != nil && submission.SigningMode.String() == "SIGNING_MODE_SEQUENTIAL" {
		s.advanceSequentialWorkflow(ctx, tenantID, session.submissionID, sub.SigningOrder)
	}

	// Check completion
	if submission != nil {
		s.checkAndCompleteSubmission(ctx, tenantID, session.submissionID, submission)
	}

	return &signingV1.CompleteBissSigningResponse{
		Completed: true,
		Message:   "Document signed with Qualified Electronic Signature (QES).",
	}, nil
}

// precomputedSigner implements crypto.Signer using a pre-computed signature.
// Used to wrap the BISS raw signature into the pkcs7 library's AddSignerChain API.
type precomputedSigner struct {
	signature []byte
	publicKey crypto.PublicKey
}

func (s *precomputedSigner) Public() crypto.PublicKey {
	return s.publicKey
}

func (s *precomputedSigner) Sign(_ io.Reader, _ []byte, _ crypto.SignerOpts) ([]byte, error) {
	// Return the pre-computed BISS signature instead of computing one
	return s.signature, nil
}

// extractByteRangeContent extracts the PDF byte range content (everything except the /Contents hex area).
func extractByteRangeContent(preparedPDF []byte) ([]byte, error) {
	contentsTag := []byte("/Contents<")
	sigStart := -1
	searchFrom := 0
	for {
		idx := bytes.Index(preparedPDF[searchFrom:], contentsTag)
		if idx < 0 {
			break
		}
		pos := searchFrom + idx + len(contentsTag)
		if pos+100 < len(preparedPDF) && preparedPDF[pos] == '0' && preparedPDF[pos+1] == '0' {
			sigStart = pos
			break
		}
		searchFrom = searchFrom + idx + 1
	}
	if sigStart < 0 {
		return nil, fmt.Errorf("could not find /Contents signature placeholder")
	}
	sigEnd := sigStart
	for sigEnd < len(preparedPDF) && preparedPDF[sigEnd] != '>' {
		sigEnd++
	}

	// Use copy to avoid modifying preparedPDF's underlying array
	byteRange1 := make([]byte, sigStart)
	copy(byteRange1, preparedPDF[:sigStart])
	byteRange2 := make([]byte, len(preparedPDF)-(sigEnd+1))
	copy(byteRange2, preparedPDF[sigEnd+1:])
	return append(byteRange1, byteRange2...), nil
}

// buildSignedAttrsDER builds the DER-encoded signedAttrs for CMS/PKCS#7.
// These attributes are what BISS actually signs (with contentType "data", BISS signs SHA256(DER_signedAttrs)).
func buildSignedAttrsDER(signContent, pdfHash []byte) ([]byte, error) {
	// Use pkcs7 library to build proper signedAttrs by creating a temp SignedData
	sd, err := pkcs7.NewSignedData(signContent)
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1})

	// Get the marshaled signedAttrs via the library's internal computation
	// by using a signer that captures what it's asked to sign
	_ = sd // not used further, just needed to verify digest
	_ = big.NewInt(0) // ensure import is used

	// Build signedAttrs manually
	oidContentType := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidData := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}

	type attribute struct {
		Type  asn1.ObjectIdentifier
		Value asn1.RawValue `asn1:"set"`
	}

	// Marshal content type value
	ctValue, _ := asn1.Marshal(oidData)
	// Marshal message digest value
	mdValue, _ := asn1.Marshal(pdfHash)

	attrs := []attribute{
		{Type: oidContentType, Value: asn1.RawValue{FullBytes: ctValue}},
		{Type: oidMessageDigest, Value: asn1.RawValue{FullBytes: mdValue}},
	}

	// Marshal as SET OF
	var attrsBytes []byte
	for _, attr := range attrs {
		b, err := asn1.Marshal(attr)
		if err != nil {
			return nil, err
		}
		attrsBytes = append(attrsBytes, b...)
	}

	// Wrap in SET tag (0x31) for DER encoding of signedAttrs
	signedAttrsDER, err := asn1.Marshal(asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSet,
		IsCompound: true,
		Bytes:      attrsBytes,
	})
	if err != nil {
		return nil, err
	}

	return signedAttrsDER, nil
}

// buildPKCS7Container builds a CMS/PKCS#7 SignedData container for PAdES.
// With contentType "data", BISS signs SHA256(DER_signedAttrs), which is exactly
// what PKCS#7 with signedAttributes expects.
func buildPKCS7Container(signContent, pdfHash, rawSignature []byte, signerCert *x509.Certificate, certChain []*x509.Certificate) ([]byte, error) {
	signedData, err := pkcs7.NewSignedData(signContent)
	if err != nil {
		return nil, fmt.Errorf("new signed data: %w", err)
	}

	signedData.SetDigestAlgorithm(asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1})

	// The precomputedSigner returns the BISS signature.
	// BISS signed SHA256(DER_signedAttrs) (since we sent signedAttrs as contents with "data" mode).
	// pkcs7.AddSignerChain computes SHA256(signContent) for messageDigest, builds signedAttrs,
	// then calls signer.Sign(SHA256(DER_signedAttrs)) → our precomputed BISS signature.
	// The BISS signature matches because it was computed over the same signedAttrs DER.
	fakeSigner := &precomputedSigner{
		signature: rawSignature,
		publicKey: signerCert.PublicKey,
	}

	var chainCerts []*x509.Certificate
	if len(certChain) > 1 {
		chainCerts = certChain[1:]
	}

	if err := signedData.AddSignerChain(signerCert, fakeSigner, chainCerts, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("add signer chain: %w", err)
	}

	signedData.Detach()

	return signedData.Finish()
}
 
 
 
 
 
 
 
 
