package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"os"
	"sync"
	"time"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pdfsign/sign"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submitter"
	appViewer "github.com/go-tangra/go-tangra-signing/pkg/viewer"
)

// bissSession stores state between PrepareForBissSigning and CompleteBissSigning.
type bissSession struct {
	draftPDF     []byte // Complete signed PDF from Pass 1 (with dummy signature)
	dummySig     []byte // The dummy signature bytes to find and replace
	submitterID  string
	submissionID string
	tenantID     uint32
	createdAt    time.Time
}

var (
	bissSessions   = make(map[string]*bissSession)
	bissSessionsMu sync.Mutex
)

func storeBissSession(id string, session *bissSession) {
	bissSessionsMu.Lock()
	defer bissSessionsMu.Unlock()
	bissSessions[id] = session
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

func loadServerCert() (*x509.Certificate, crypto.Signer, error) {
	certPath := os.Getenv("BISS_CERT_PATH")
	keyPath := os.Getenv("BISS_KEY_PATH")
	if certPath == "" || keyPath == "" {
		certsDir := os.Getenv("CERTS_DIR")
		if certsDir == "" {
			certsDir = "/app/certs"
		}
		if _, err := os.Stat(certsDir + "/biss/cert.pem"); err == nil {
			certPath = certsDir + "/biss/cert.pem"
			keyPath = certsDir + "/biss/key.pem"
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

// captureSigner captures the digest and returns a deterministic dummy signature.
type captureSigner struct {
	publicKey crypto.PublicKey
	dummySig  []byte
	digest    []byte
}

func (s *captureSigner) Public() crypto.PublicKey { return s.publicKey }
func (s *captureSigner) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	s.digest = make([]byte, len(digest))
	copy(s.digest, digest)
	return s.dummySig, nil
}

// replaySigner returns a pre-computed signature (from BISS).
type replaySigner struct {
	publicKey crypto.PublicKey
	signature []byte
}

func (s *replaySigner) Public() crypto.PublicKey { return s.publicKey }
func (s *replaySigner) Sign(_ io.Reader, _ []byte, _ crypto.SignerOpts) ([]byte, error) {
	return s.signature, nil
}

// PrepareForBissSigning: Pass 1 — overlay fields, call sign.Sign with captureSigner to get the
// 32-byte digest (SHA256 of signedAttrs), then send it to the frontend for BISS signing.
func (s *SessionService) PrepareForBissSigning(ctx context.Context, req *signingV1.PrepareForBissSigningRequest) (*signingV1.PrepareForBissSigningResponse, error) {
	ctx = appViewer.NewSystemViewerContext(ctx)

	if req.Token == "" {
		return nil, signingV1.ErrorBadRequest("token is required")
	}

	sub, err := s.submitterRepo.GetBySlug(ctx, req.Token)
	if err != nil || sub == nil {
		return nil, signingV1.ErrorSubmitterNotFound("signing session not found")
	}
	if sub.Status == submitter.StatusSUBMITTER_STATUS_COMPLETED {
		return nil, signingV1.ErrorSubmitterAlreadyCompleted("already completed")
	}

	submission, err := s.submissionRepo.GetByID(ctx, sub.SubmissionID)
	if err != nil || submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	template, err := s.templateRepo.GetByID(ctx, submission.TemplateID)
	if err != nil || template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	pdfKey := template.FileKey
	if submission.CurrentPdfKey != "" {
		pdfKey = submission.CurrentPdfKey
	}
	pdfContent, err := s.storage.Download(ctx, pdfKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	// Overlay field values — but SKIP if the PDF already has a PAdES signature.
	// Modifying a PAdES-signed PDF with pdfcpu would invalidate the previous signer's signature.
	hasPAdES := bytes.Contains(pdfContent, []byte("/Type /Sig")) && bytes.Contains(pdfContent, []byte("/SubFilter /adbe.pkcs7.detached"))
	if !hasPAdES {
		values := fieldValuesToMap(req.FieldValues)
		fieldValues := buildFieldValuesForOverlay(submission.TemplateFieldsSnapshot, sub.SigningOrder, values)
		if len(fieldValues) > 0 {
			if overlaid, err := s.pdfGenerator.overlayFields(pdfContent, fieldValues); err == nil {
				pdfContent = overlaid
			}
		}
	}

	// Parse signer certificate chain
	var signerCert *x509.Certificate
	var signerChain []*x509.Certificate
	for _, certB64 := range req.SignerCertificateChain {
		if certBytes, err := base64.StdEncoding.DecodeString(certB64); err == nil {
			if cert, err := x509.ParseCertificate(certBytes); err == nil {
				signerChain = append(signerChain, cert)
			}
		}
	}
	if len(signerChain) == 0 {
		return nil, signingV1.ErrorBadRequest("signer certificate required")
	}
	signerCert = signerChain[0]

	// Fixed date for both passes (must be identical for digest to match)
	fixedDate := time.Now().UTC().Truncate(time.Second)

	// Extract signer name from certificate CN
	signerName := signerCert.Subject.CommonName
	if signerName == "" {
		signerName = sub.Name
	}

	// Find the signature field position from the template snapshot
	sigAppearance := sign.Appearance{Visible: false}
	for _, f := range submission.TemplateFieldsSnapshot {
		fType := getStringField(f, "type")
		fIdx := getIntField(f, "submitter_index")
		if (fType == "signature" || fType == "initials") && fIdx == sub.SigningOrder {
			// Convert CSS percentage coords (top-left origin) to PDF points (bottom-left origin)
			// A4: 595.28 x 841.89 points
			const pageW, pageH = 595.28, 841.89
			xPct := getFloat64Field(f, "x_percent")
			yPct := getFloat64Field(f, "y_percent")
			pgNum := getIntField(f, "page_number")
			if pgNum <= 0 {
				pgNum = 1
			}

			x := xPct / 100.0 * pageW
			yTop := yPct / 100.0 * pageH
			stampW := 200.0
			stampH := 60.0
			yBottom := pageH - yTop - stampH

			// Generate signature stamp image with proper text rendering
			stampImg := generateSignatureStampImage(signerName, signerCert.Issuer.CommonName, fixedDate)

			sigAppearance = sign.Appearance{
				Visible:     true,
				Page:        uint32(pgNum),
				LowerLeftX:  x,
				LowerLeftY:  yBottom,
				UpperRightX: x + stampW,
				UpperRightY: yBottom + stampH,
				Image:       stampImg,
			}
			break
		}
	}

	signData := sign.SignData{
		Certificate: signerCert,
		Signature: sign.SignDataSignature{
			CertType:   sign.ApprovalSignature,
			DocMDPPerm: sign.AllowFillingExistingFormFieldsAndSignaturesPerms,
			Info: sign.SignDataSignatureInfo{
				Name:     signerName,
				Location: "GoTangra Signing",
				Reason:   "Document signing",
				Date:     fixedDate,
			},
		},
		DigestAlgorithm: crypto.SHA256,
		Appearance:      sigAppearance,
	}

	// Add certificate chain
	if len(signerChain) > 1 {
		signData.CertificateChains = [][]*x509.Certificate{signerChain}
	}

	// Use a deterministic dummy signature matching the signer's key size
	sigSize := 256 // default RSA-2048
	if pub, ok := signerCert.PublicKey.(*rsa.PublicKey); ok {
		sigSize = (pub.N.BitLen() + 7) / 8
	}
	dummySig := bytes.Repeat([]byte{0xAB}, sigSize)
	capture := &captureSigner{publicKey: signerCert.PublicKey, dummySig: dummySig}
	signData.Signer = capture

	rdr, err := pdf.NewReader(bytes.NewReader(pdfContent), int64(len(pdfContent)))
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF: %w", err)
	}

	var draftOut bytes.Buffer
	if err := sign.Sign(bytes.NewReader(pdfContent), &draftOut, rdr, int64(len(pdfContent)), signData); err != nil {
		return nil, fmt.Errorf("pass 1 sign failed: %w", err)
	}

	if len(capture.digest) == 0 {
		return nil, fmt.Errorf("failed to capture digest from pass 1")
	}

	// Extract the signedAttrs DER from the draft PDF's PKCS#7.
	// Send it to BISS with contentType "data" — BISS signs SHA256(signedAttrs_DER)
	// which is EXACTLY what PKCS#7 with signedAttributes expects.
	draftPKCS7 := extractPKCS7FromPDF(draftOut.Bytes())
	signedAttrsDER := extractSignedAttrsDER(draftPKCS7)
	if len(signedAttrsDER) == 0 {
		// Fallback to captured digest with contentType "digest"
		signedAttrsDER = capture.digest
	}
	bissContent := signedAttrsDER

	// Server cert signature for BISS signedContents verification
	serverCert, serverSigner, err := loadServerCert()
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	// BISS verifies: SHA256(SHA256(content) || content) signed with server cert
	contentHash := sha256.Sum256(bissContent)
	combined := append(contentHash[:], bissContent...)
	finalDigest := sha256.Sum256(combined)
	signedHash, err := serverSigner.Sign(rand.Reader, finalDigest[:], crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("failed to sign with server cert: %w", err)
	}

	sessionID := generateUUID()
	tenantID := derefUint32(sub.TenantID)

	storeBissSession(sessionID, &bissSession{
		draftPDF:     draftOut.Bytes(),
		dummySig:     dummySig,
		submitterID:  sub.ID,
		submissionID: submission.ID,
		tenantID:     tenantID,
		createdAt:    time.Now(),
	})

	return &signingV1.PrepareForBissSigningResponse{
		HashBase64:       base64.StdEncoding.EncodeToString(bissContent),
		SignedHashBase64: base64.StdEncoding.EncodeToString(signedHash),
		ServerCertBase64: base64.StdEncoding.EncodeToString(serverCert.Raw),
		SessionId:        sessionID,
	}, nil
}

// CompleteBissSigning: Pass 2 — call sign.Sign with replaySigner that returns the BISS signature.
func (s *SessionService) CompleteBissSigning(ctx context.Context, req *signingV1.CompleteBissSigningRequest) (*signingV1.CompleteBissSigningResponse, error) {
	ctx = appViewer.NewSystemViewerContext(ctx)

	if req.Token == "" || req.SessionId == "" || req.Pkcs7SignatureBase64 == "" {
		return nil, signingV1.ErrorBadRequest("token, session_id, and pkcs7_signature_base64 are required")
	}

	session := loadBissSession(req.SessionId)
	if session == nil {
		return nil, signingV1.ErrorBadRequest("BISS session expired or not found")
	}
	deleteBissSession(req.SessionId)

	sub, err := s.submitterRepo.GetBySlug(ctx, req.Token)
	if err != nil || sub == nil {
		return nil, signingV1.ErrorSubmitterNotFound("signing session not found")
	}
	if sub.ID != session.submitterID {
		return nil, signingV1.ErrorBadRequest("token does not match BISS session")
	}

	rawSignature, err := base64.StdEncoding.DecodeString(req.Pkcs7SignatureBase64)
	if err != nil {
		return nil, signingV1.ErrorBadRequest("invalid signature encoding")
	}

	// Patch the dummy signature in the draft PDF with the real BISS signature.
	// The draft PDF has the exact same signedAttrs (same signingTime) that BISS signed.
	signedPDF := make([]byte, len(session.draftPDF))
	copy(signedPDF, session.draftPDF)

	// The dummy signature is hex-encoded inside the PDF's /Contents<...>
	// Find the hex-encoded dummy pattern
	dummyHex := make([]byte, len(session.dummySig)*2)
	for i, b := range session.dummySig {
		dummyHex[i*2] = "0123456789abcdef"[b>>4]
		dummyHex[i*2+1] = "0123456789abcdef"[b&0xf]
	}

	realHex := make([]byte, len(rawSignature)*2)
	for i, b := range rawSignature {
		realHex[i*2] = "0123456789abcdef"[b>>4]
		realHex[i*2+1] = "0123456789abcdef"[b&0xf]
	}

	idx := bytes.Index(signedPDF, dummyHex)
	if idx < 0 {
		return nil, fmt.Errorf("could not find dummy signature in draft PDF")
	}

	// Replace dummy hex with real hex (pad with zeros if BISS sig is shorter)
	copy(signedPDF[idx:], realHex)
	if len(realHex) < len(dummyHex) {
		for i := len(realHex); i < len(dummyHex); i++ {
			signedPDF[idx+i] = '0'
		}
	}

	// Upload
	signedKey := fmt.Sprintf("%d/signed/%s/biss-signed.pdf", session.tenantID, session.submissionID)
	if _, err := s.storage.UploadRaw(ctx, signedKey, signedPDF, "application/pdf"); err != nil {
		return nil, fmt.Errorf("failed to upload signed PDF: %w", err)
	}

	// Mark submitter completed
	now := time.Now()
	valuesMap := map[string]interface{}{"_biss_signed": true, "_signed_document_key": signedKey}
	if _, err := s.submitterRepo.Complete(ctx, sub.ID, valuesMap, "", "", now); err != nil {
		return nil, err
	}

	if err := s.submissionRepo.UpdateSignedDocumentKey(ctx, session.submissionID, signedKey); err != nil {
		s.log.Errorf("failed to update signed document key: %v", err)
	}
	if err := s.submissionRepo.UpdateCurrentPdfKey(ctx, session.submissionID, signedKey); err != nil {
		s.log.Errorf("failed to update current_pdf_key: %v", err)
	}

	_ = s.eventRepo.Create(ctx, session.tenantID, "submitter.biss_signed", "",
		"submitter", sub.ID, map[string]interface{}{"signed_key": signedKey}, "")

	submission, _ := s.submissionRepo.GetByID(ctx, session.submissionID)
	if submission != nil && submission.SigningMode.String() == "SIGNING_MODE_SEQUENTIAL" {
		s.advanceSequentialWorkflow(ctx, session.tenantID, session.submissionID, sub.SigningOrder)
	}
	if submission != nil {
		s.checkAndCompleteSubmission(ctx, session.tenantID, session.submissionID, submission)
	}

	return &signingV1.CompleteBissSigningResponse{
		Completed: true,
		Message:   "Document signed with Qualified Electronic Signature (QES).",
	}, nil
}

// generateSignatureStampImage creates a PNG image resembling Adobe's signature appearance:
// Left side: signer name in large text (split into lines)
// Right side: "Digitally signed by [name]" + "Date: [date]" in small text
func generateSignatureStampImage(signerName, issuerName string, signDate time.Time) []byte {
	width, height := 600, 180

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Light gray background with thin border
	bgColor := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	borderColor := color.RGBA{R: 180, G: 180, B: 180, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x == 0 || x == width-1 || y == 0 || y == height-1 {
				img.Set(x, y, borderColor)
			} else {
				img.Set(x, y, bgColor)
			}
		}
	}

	// Use Go's built-in font for text rendering
	textColor := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	smallColor := color.RGBA{R: 120, G: 40, B: 40, A: 255} // Dark red for "Digitally signed by"

	// Draw text using golang.org/x/image/font/basicfont
	drawBasicText(img, signerName, 10, 40, textColor, 3)     // Large name - left side

	// Right side text
	midX := width / 2
	drawBasicText(img, "Digitally signed by", midX+10, 25, smallColor, 1)

	// Split name into lines for the right side
	nameParts := splitName(signerName)
	yPos := 45
	for _, part := range nameParts {
		drawBasicText(img, part, midX+20, yPos, smallColor, 1)
		yPos += 18
	}

	dateStr := "Date: " + signDate.Format("2006.01.02")
	timeStr := signDate.Format("15:04:05 -07'00'")
	drawBasicText(img, dateStr, midX+10, yPos, smallColor, 1)
	drawBasicText(img, timeStr, midX+20, yPos+18, smallColor, 1)

	// Vertical divider line
	divColor := color.RGBA{R: 200, G: 200, B: 200, A: 255}
	for y := 10; y < height-10; y++ {
		img.Set(midX, y, divColor)
	}

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// drawBasicText draws text using golang.org/x/image/font/basicfont at a given scale
func drawBasicText(img *image.RGBA, text string, x, y int, c color.RGBA, scale int) {
	face := basicfont.Face7x13
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	if scale > 1 {
		// For larger text, draw multiple times offset by 1px
		for dy := 0; dy < scale; dy++ {
			for dx := 0; dx < scale; dx++ {
				d.Dot = fixed.P(x+dx, y+dy)
				d.DrawString(text)
			}
		}
	} else {
		d.DrawString(text)
	}
}

// splitName splits a full name into parts for multi-line display
func splitName(name string) []string {
	words := bytes.Fields([]byte(name))
	if len(words) <= 2 {
		return []string{name}
	}
	var parts []string
	for _, w := range words {
		parts = append(parts, string(w))
	}
	return parts
}

// extractPKCS7FromPDF extracts the raw PKCS#7 DER bytes from a signed PDF's /Contents<hex>.
func extractPKCS7FromPDF(pdfData []byte) []byte {
	contentsTag := []byte("/Contents<")
	searchFrom := 0
	for {
		idx := bytes.Index(pdfData[searchFrom:], contentsTag)
		if idx < 0 {
			return nil
		}
		pos := searchFrom + idx + len(contentsTag)
		if pos < len(pdfData) && pdfData[pos] != '0' {
			end := pos
			for end < len(pdfData) && pdfData[end] != '>' {
				end++
			}
			hexData := bytes.TrimRight(pdfData[pos:end], "0")
			if len(hexData)%2 == 1 {
				hexData = append(hexData, '0')
			}
			der := make([]byte, len(hexData)/2)
			for i := 0; i < len(hexData); i += 2 {
				der[i/2] = unhex(hexData[i])<<4 | unhex(hexData[i+1])
			}
			return der
		}
		searchFrom = searchFrom + idx + 1
	}
}

func unhex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}

// extractSignedAttrsDER extracts the DER-encoded signedAttrs from a PKCS#7 DER.
// Returns with tag 0x31 (SET) for hash computation per CMS spec.
func extractSignedAttrsDER(pkcs7DER []byte) []byte {
	for i := 0; i < len(pkcs7DER)-10; i++ {
		if pkcs7DER[i] == 0xA0 {
			length := 0
			headerLen := 0
			if pkcs7DER[i+1] < 0x80 {
				length = int(pkcs7DER[i+1])
				headerLen = 2
			} else if pkcs7DER[i+1] == 0x81 {
				length = int(pkcs7DER[i+2])
				headerLen = 3
			} else if pkcs7DER[i+1] == 0x82 {
				length = int(pkcs7DER[i+2])<<8 | int(pkcs7DER[i+3])
				headerLen = 4
			}
			if length > 50 && length < 500 && i+headerLen+length <= len(pkcs7DER) {
				attrs := pkcs7DER[i : i+headerLen+length]
				contentTypeOID := []byte{0x06, 0x09, 0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x09, 0x03}
				if bytes.Contains(attrs, contentTypeOID) {
					result := make([]byte, len(attrs))
					copy(result, attrs)
					result[0] = 0x31 // Change [0] to SET for hash computation
					return result
				}
			}
		}
	}
	return nil
}
