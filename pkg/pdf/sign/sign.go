// Package sign implements PDF digital signing with PKCS7/CMS and PAdES support.
// Ported from github.com/shurco/goSign with adaptations for the go-tangra ecosystem.
package sign

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pkcs7"

	"github.com/go-tangra/go-tangra-signing/pkg/pdf/revocation"
)

type CatalogData struct {
	ObjectId   uint32
	Length     int64
	RootString string
}

type TSA struct {
	URL      string
	Username string
	Password string
}

type RevocationFunction func(cert, issuer *x509.Certificate, i *revocation.InfoArchival) error

type SignData struct {
	ObjectId           uint32
	Signature          SignDataSignature
	Signer             crypto.Signer
	DigestAlgorithm    crypto.Hash
	Certificate        *x509.Certificate
	CertificateChains  [][]*x509.Certificate
	TSA                TSA
	RevocationData     revocation.InfoArchival
	RevocationFunction RevocationFunction
}

type VisualSignData struct {
	ObjectId uint32
	Length   int64
}

type InfoData struct {
	ObjectId uint32
	Length   int64
}

type SignDataSignature struct {
	CertType   uint
	DocMDPPerm uint
	Info       SignDataSignatureInfo
}

const (
	CertificationSignature = iota + 1
	ApprovalSignature
	UsageRightsSignature
)

const (
	DoNotAllowAnyChangesPerms = iota + 1
	AllowFillingExistingFormFieldsAndSignaturesPerms
	AllowFillingExistingFormFieldsAndSignaturesAndCRUDAnnotationsPerms
)

type SignDataSignatureInfo struct {
	Name        string
	Location    string
	Reason      string
	ContactInfo string
	Date        time.Time
}

type SignContext struct {
	Filesize                   int64
	InputFile                  io.ReadSeeker
	OutputFile                 io.Writer
	OutputBuffer               *bytes.Buffer
	SignData                   SignData
	CatalogData                CatalogData
	VisualSignData             VisualSignData
	InfoData                   InfoData
	PDFReader                  *pdf.Reader
	NewXrefStart               int64
	ByteRangeStartByte         int64
	SignatureContentsStartByte int64
	ByteRangeValues            []int64
	SignatureMaxLength         uint32
	SignatureMaxLengthBase     uint32
}

// Sign signs a PDF from an io.ReadSeeker and writes the signed result to an io.Writer.
func Sign(input io.ReadSeeker, output io.Writer, rdr *pdf.Reader, size int64, signData SignData) error {
	if rdr == nil {
		var err error
		content, err := io.ReadAll(input)
		if err != nil {
			return fmt.Errorf("failed to read PDF input: %w", err)
		}
		if _, err = input.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek PDF input: %w", err)
		}
		rdr, err = pdf.NewReader(bytes.NewReader(content), size)
		if err != nil {
			return fmt.Errorf("failed to create PDF reader: %w", err)
		}
	}

	signData.ObjectId = uint32(rdr.XrefInformation.ItemCount) + 3

	ctx := SignContext{
		Filesize:   size + 1, // +1 for the newline we insert
		PDFReader:  rdr,
		InputFile:  input,
		OutputFile: output,
		VisualSignData: VisualSignData{
			ObjectId: uint32(rdr.XrefInformation.ItemCount),
		},
		CatalogData: CatalogData{
			ObjectId: uint32(rdr.XrefInformation.ItemCount) + 1,
		},
		InfoData: InfoData{
			ObjectId: uint32(rdr.XrefInformation.ItemCount) + 2,
		},
		SignData:               signData,
		SignatureMaxLengthBase: uint32(hex.EncodedLen(512)),
	}

	return ctx.SignPDF()
}

// SignPDF performs the full PDF signing process.
func (ctx *SignContext) SignPDF() error {
	// Set defaults
	if ctx.SignData.Signature.CertType == 0 {
		ctx.SignData.Signature.CertType = CertificationSignature
	}
	if ctx.SignData.Signature.DocMDPPerm == 0 {
		ctx.SignData.Signature.DocMDPPerm = DoNotAllowAnyChangesPerms
	}
	if !ctx.SignData.DigestAlgorithm.Available() {
		ctx.SignData.DigestAlgorithm = crypto.SHA256
	}

	ctx.OutputBuffer = &bytes.Buffer{}

	// Copy original file
	if _, err := ctx.InputFile.Seek(0, 0); err != nil {
		return err
	}
	if _, err := io.Copy(ctx.OutputBuffer, ctx.InputFile); err != nil {
		return err
	}

	// Append newline after %%EOF
	if _, err := ctx.OutputBuffer.Write([]byte("\n")); err != nil {
		return err
	}

	// Calculate signature space
	ctx.SignatureMaxLength = ctx.SignatureMaxLengthBase
	ctx.calculateSignatureSpace()

	// Fetch revocation data
	if err := ctx.fetchRevocationData(); err != nil {
		return fmt.Errorf("failed to fetch revocation data: %w", err)
	}

	// Create PDF objects
	visualSig, err := ctx.createVisualSignature()
	if err != nil {
		return fmt.Errorf("failed to create visual signature: %w", err)
	}
	ctx.VisualSignData.Length = int64(len(visualSig))
	if _, err := ctx.OutputBuffer.Write([]byte(visualSig)); err != nil {
		return err
	}

	catalog, err := ctx.createCatalog()
	if err != nil {
		return fmt.Errorf("failed to create catalog: %w", err)
	}
	ctx.CatalogData.Length = int64(len(catalog))
	if _, err := ctx.OutputBuffer.Write([]byte(catalog)); err != nil {
		return err
	}

	sigObj, byteRangeStart, sigContentsStart := ctx.createSignaturePlaceholder()

	info, err := ctx.createInfo()
	if err != nil {
		return fmt.Errorf("failed to create info: %w", err)
	}
	ctx.InfoData.Length = int64(len(info))
	if _, err := ctx.OutputBuffer.Write([]byte(info)); err != nil {
		return err
	}

	appendedBytes := ctx.Filesize + int64(len(catalog)) + int64(len(visualSig)) + int64(len(info))
	byteRangeStart += appendedBytes
	sigContentsStart += appendedBytes

	ctx.ByteRangeStartByte = byteRangeStart
	ctx.SignatureContentsStartByte = sigContentsStart

	if _, err := ctx.OutputBuffer.Write([]byte(sigObj)); err != nil {
		return err
	}

	ctx.NewXrefStart = appendedBytes + int64(len(sigObj))

	if err := ctx.writeXref(); err != nil {
		return fmt.Errorf("failed to write xref: %w", err)
	}

	if err := ctx.writeTrailer(); err != nil {
		return fmt.Errorf("failed to write trailer: %w", err)
	}

	if err := ctx.updateByteRange(); err != nil {
		return fmt.Errorf("failed to update byte range: %w", err)
	}

	if err := ctx.replaceSignature(); err != nil {
		return fmt.Errorf("failed to replace signature: %w", err)
	}

	_, err = ctx.OutputFile.Write(ctx.OutputBuffer.Bytes())
	return err
}

// PrepareForExternalSigning creates a PDF with a signature placeholder and returns
// the prepared PDF bytes and the SHA-256 hash of the byte ranges that need to be signed.
// This is the first phase of two-phase signing (for BISS/external signers).
func PrepareForExternalSigning(input io.ReadSeeker, size int64, signData SignData) (preparedPDF []byte, hash []byte, err error) {
	content, err := io.ReadAll(input)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read PDF input: %w", err)
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("failed to seek PDF input: %w", err)
	}
	rdr, err := pdf.NewReader(bytes.NewReader(content), size)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create PDF reader: %w", err)
	}

	signData.ObjectId = uint32(rdr.XrefInformation.ItemCount) + 3

	// Reserve large space for external PKCS#7 (BISS signatures can be large)
	maxLenBase := uint32(hex.EncodedLen(16384))

	ctx := SignContext{
		Filesize:   size + 1,
		PDFReader:  rdr,
		InputFile:  input,
		OutputFile: io.Discard,
		VisualSignData: VisualSignData{
			ObjectId: uint32(rdr.XrefInformation.ItemCount),
		},
		CatalogData: CatalogData{
			ObjectId: uint32(rdr.XrefInformation.ItemCount) + 1,
		},
		InfoData: InfoData{
			ObjectId: uint32(rdr.XrefInformation.ItemCount) + 2,
		},
		SignData:               signData,
		SignatureMaxLengthBase: maxLenBase,
	}

	// Prepare the PDF (everything except the actual signature)
	if err := ctx.preparePDF(); err != nil {
		return nil, nil, fmt.Errorf("failed to prepare PDF: %w", err)
	}

	// Compute hash of the byte ranges
	fileContent := ctx.OutputBuffer.Bytes()
	signContent := make([]byte, 0)
	signContent = append(signContent, fileContent[ctx.ByteRangeValues[0]:(ctx.ByteRangeValues[0]+ctx.ByteRangeValues[1])]...)
	signContent = append(signContent, fileContent[ctx.ByteRangeValues[2]:(ctx.ByteRangeValues[2]+ctx.ByteRangeValues[3])]...)

	h := crypto.SHA256.New()
	h.Write(signContent)
	hashBytes := h.Sum(nil)

	return ctx.OutputBuffer.Bytes(), hashBytes, nil
}

// EmbedExternalSignature takes a prepared PDF (from PrepareForExternalSigning) and embeds
// a raw PKCS#7/CMS signature into the /Contents placeholder.
// This is the second phase of two-phase signing.
func EmbedExternalSignature(preparedPDF []byte, pkcs7Signature []byte) ([]byte, error) {
	dst := make([]byte, hex.EncodedLen(len(pkcs7Signature)))
	hex.Encode(dst, pkcs7Signature)

	// Find the signature placeholder: look for /Contents< followed by a long run of zeros.
	// We search for the signature-specific /Contents (not a page /Contents reference)
	// by looking for /Contents< followed by at least 100 zeros.
	contentsTag := []byte("/Contents<")
	sigStart := -1
	searchFrom := 0
	for {
		idx := bytes.Index(preparedPDF[searchFrom:], contentsTag)
		if idx < 0 {
			break
		}
		pos := searchFrom + idx + len(contentsTag)
		// Check if this is followed by a long run of zeros (the placeholder)
		if pos+100 < len(preparedPDF) && preparedPDF[pos] == '0' && preparedPDF[pos+1] == '0' && preparedPDF[pos+99] == '0' {
			sigStart = pos
			break
		}
		searchFrom = searchFrom + idx + 1
	}
	if sigStart < 0 {
		return nil, fmt.Errorf("could not find /Contents signature placeholder in prepared PDF")
	}

	// Find the end of the placeholder (closing >)
	sigEnd := sigStart
	for sigEnd < len(preparedPDF) && preparedPDF[sigEnd] != '>' {
		sigEnd++
	}
	if sigEnd >= len(preparedPDF) {
		return nil, fmt.Errorf("could not find end of /Contents placeholder")
	}

	placeholderLen := sigEnd - sigStart
	if len(dst) > placeholderLen {
		return nil, fmt.Errorf("signature too large: %d bytes > %d placeholder bytes", len(dst), placeholderLen)
	}

	// Build the output: copy everything, replace the placeholder with the hex-encoded signature
	result := make([]byte, len(preparedPDF))
	copy(result, preparedPDF)

	// Write the hex signature at the placeholder start
	copy(result[sigStart:], dst)
	// Pad remaining space with zeros (already zeros from prepare, but be safe)
	for i := sigStart + len(dst); i < sigEnd; i++ {
		result[i] = '0'
	}

	return result, nil
}

// preparePDF does everything SignPDF does except creating and embedding the actual signature.
func (ctx *SignContext) preparePDF() error {
	if ctx.SignData.Signature.CertType == 0 {
		ctx.SignData.Signature.CertType = CertificationSignature
	}
	if ctx.SignData.Signature.DocMDPPerm == 0 {
		ctx.SignData.Signature.DocMDPPerm = DoNotAllowAnyChangesPerms
	}
	if !ctx.SignData.DigestAlgorithm.Available() {
		ctx.SignData.DigestAlgorithm = crypto.SHA256
	}

	ctx.OutputBuffer = &bytes.Buffer{}

	if _, err := ctx.InputFile.Seek(0, 0); err != nil {
		return err
	}
	if _, err := io.Copy(ctx.OutputBuffer, ctx.InputFile); err != nil {
		return err
	}
	if _, err := ctx.OutputBuffer.Write([]byte("\n")); err != nil {
		return err
	}

	// Set signature space - use base + extra for PKCS#7 container
	ctx.SignatureMaxLength = ctx.SignatureMaxLengthBase
	ctx.calculateSignatureSpace()

	visualSig, err := ctx.createVisualSignature()
	if err != nil {
		return fmt.Errorf("failed to create visual signature: %w", err)
	}
	ctx.VisualSignData.Length = int64(len(visualSig))
	if _, err := ctx.OutputBuffer.Write([]byte(visualSig)); err != nil {
		return err
	}

	catalog, err := ctx.createCatalog()
	if err != nil {
		return fmt.Errorf("failed to create catalog: %w", err)
	}
	ctx.CatalogData.Length = int64(len(catalog))
	if _, err := ctx.OutputBuffer.Write([]byte(catalog)); err != nil {
		return err
	}

	sigObj, byteRangeStart, sigContentsStart := ctx.createSignaturePlaceholder()

	info, err := ctx.createInfo()
	if err != nil {
		return fmt.Errorf("failed to create info: %w", err)
	}
	ctx.InfoData.Length = int64(len(info))
	if _, err := ctx.OutputBuffer.Write([]byte(info)); err != nil {
		return err
	}

	appendedBytes := ctx.Filesize + int64(len(catalog)) + int64(len(visualSig)) + int64(len(info))
	byteRangeStart += appendedBytes
	sigContentsStart += appendedBytes

	ctx.ByteRangeStartByte = byteRangeStart
	ctx.SignatureContentsStartByte = sigContentsStart

	if _, err := ctx.OutputBuffer.Write([]byte(sigObj)); err != nil {
		return err
	}

	ctx.NewXrefStart = appendedBytes + int64(len(sigObj))

	if err := ctx.writeXref(); err != nil {
		return fmt.Errorf("failed to write xref: %w", err)
	}
	if err := ctx.writeTrailer(); err != nil {
		return fmt.Errorf("failed to write trailer: %w", err)
	}
	if err := ctx.updateByteRange(); err != nil {
		return fmt.Errorf("failed to update byte range: %w", err)
	}

	return nil
}

// calculateSignatureSpace estimates the space needed for the PKCS7 signature.
func (ctx *SignContext) calculateSignatureSpace() {
	// Add digest algorithm size
	ctx.SignatureMaxLength += uint32(hex.EncodedLen(ctx.SignData.DigestAlgorithm.Size() * 2))

	// Add certificate size
	if ctx.SignData.Certificate != nil {
		degenerated, err := pkcs7.DegenerateCertificate(ctx.SignData.Certificate.Raw)
		if err == nil {
			ctx.SignatureMaxLength += uint32(hex.EncodedLen(len(degenerated)))
		}
	}

	// Add certificate chain size
	if len(ctx.SignData.CertificateChains) > 0 && len(ctx.SignData.CertificateChains[0]) > 1 {
		for _, cert := range ctx.SignData.CertificateChains[0][1:] {
			degenerated, err := pkcs7.DegenerateCertificate(cert.Raw)
			if err == nil {
				ctx.SignatureMaxLength += uint32(hex.EncodedLen(len(degenerated)))
			}
		}
	}

	// Add TSA estimated size
	if ctx.SignData.TSA.URL != "" {
		ctx.SignatureMaxLength += uint32(hex.EncodedLen(9000))
	}
}

// DefaultEmbedRevocationStatusFunction fetches OCSP/CRL data for embedding in the signature.
func DefaultEmbedRevocationStatusFunction(cert, issuer *x509.Certificate, i *revocation.InfoArchival) error {
	if issuer != nil {
		if err := revocation.EmbedOCSPRevocationStatus(cert, issuer, i); err != nil {
			// OCSP failed, try CRL
			if crlErr := revocation.EmbedCRLRevocationStatus(cert, i); crlErr != nil {
				return fmt.Errorf("failed to embed revocation status: OCSP: %w, CRL: %v", err, crlErr)
			}
		}
	} else {
		// No issuer, try CRL only
		_ = revocation.EmbedCRLRevocationStatus(cert, i)
	}
	return nil
}
