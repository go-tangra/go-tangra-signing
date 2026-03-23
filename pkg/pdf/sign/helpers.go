package sign

import (
	"bytes"
	"crypto"
	"encoding/asn1"
	"fmt"
	"strconv"
	"time"
	"unicode/utf16"
)

// pdfString encodes a string for PDF using UTF-16BE with BOM if needed.
func pdfString(s string) string {
	needsUnicode := false
	for _, r := range s {
		if r > 127 {
			needsUnicode = true
			break
		}
	}

	if !needsUnicode {
		return "(" + s + ")"
	}

	// UTF-16BE encoding with BOM
	encoded := utf16.Encode([]rune(s))
	var buf bytes.Buffer
	buf.WriteString("<FEFF") // BOM
	for _, r := range encoded {
		buf.WriteString(fmt.Sprintf("%04X", r))
	}
	buf.WriteString(">")
	return buf.String()
}

// pdfDateTime formats a time as a PDF date string.
func pdfDateTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return "(D:" + t.Format("20060102150405") + "+00'00')"
}

// getOIDFromHashAlgorithm returns the ASN.1 OID for a hash algorithm.
func getOIDFromHashAlgorithm(hash crypto.Hash) asn1.ObjectIdentifier {
	switch hash {
	case crypto.SHA1:
		return asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	case crypto.SHA256:
		return asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	case crypto.SHA384:
		return asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	case crypto.SHA512:
		return asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	default:
		return asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1} // Default SHA256
	}
}

// createVisualSignature creates the widget annotation object for the signature field.
func (ctx *SignContext) createVisualSignature() (string, error) {
	var buf bytes.Buffer

	// PageList not available in pdf v0.2.0; page reference left empty
	pageRef := ""

	buf.WriteString(strconv.Itoa(int(ctx.VisualSignData.ObjectId)) + " 0 obj\n")
	buf.WriteString("<< /Type /Annot")
	buf.WriteString(" /Subtype /Widget")
	buf.WriteString(" /Rect [0 0 0 0]") // Invisible annotation
	if pageRef != "" {
		buf.WriteString(" /P " + pageRef)
	}
	buf.WriteString(" /F 132")
	buf.WriteString(" /FT /Sig")
	buf.WriteString(" /T (Signature)")
	buf.WriteString(" /Ff 0")
	buf.WriteString(" /V " + strconv.Itoa(int(ctx.SignData.ObjectId)) + " 0 R")
	buf.WriteString(" >>")
	buf.WriteString("\nendobj\n")

	return buf.String(), nil
}

// createCatalog creates the updated document catalog with AcroForm.
func (ctx *SignContext) createCatalog() (string, error) {
	var buf bytes.Buffer

	buf.WriteString(strconv.Itoa(int(ctx.CatalogData.ObjectId)) + " 0 obj\n")
	buf.WriteString("<< /Type /Catalog")

	// Read Pages and Version from the original catalog via PDFReader
	root := ctx.PDFReader.Trailer().Key("Root")
	rootPtr := root.GetPtr()
	ctx.CatalogData.RootString = strconv.Itoa(int(rootPtr.GetID())) + " " + strconv.Itoa(int(rootPtr.GetGen())) + " R"

	if pages := root.Key("Pages"); pages.GetPtr().GetID() > 0 {
		p := pages.GetPtr()
		buf.WriteString(" /Pages " + strconv.Itoa(int(p.GetID())) + " " + strconv.Itoa(int(p.GetGen())) + " R")
	}

	// AcroForm with signature field
	buf.WriteString(" /AcroForm <<")
	buf.WriteString(" /Fields [" + strconv.Itoa(int(ctx.VisualSignData.ObjectId)) + " 0 R]")

	switch ctx.SignData.Signature.CertType {
	case CertificationSignature:
		buf.WriteString(" /SigFlags 3")
	default:
		buf.WriteString(" /SigFlags 1")
	}

	buf.WriteString(" /NeedAppearances false")
	buf.WriteString(" >>")

	// Perms for certification signatures
	if ctx.SignData.Signature.CertType == CertificationSignature {
		buf.WriteString(" /Perms << /DocMDP " + strconv.Itoa(int(ctx.SignData.ObjectId)) + " 0 R >>")
	}

	buf.WriteString(" >>")
	buf.WriteString("\nendobj\n")

	return buf.String(), nil
}

// createInfo creates the updated document info dictionary.
func (ctx *SignContext) createInfo() (string, error) {
	var buf bytes.Buffer

	buf.WriteString(strconv.Itoa(int(ctx.InfoData.ObjectId)) + " 0 obj\n")
	buf.WriteString("<< /ModDate " + pdfDateTime(time.Now()))
	buf.WriteString(" >>")
	buf.WriteString("\nendobj\n")

	return buf.String(), nil
}

// writeXref writes the cross-reference table for the new objects.
func (ctx *SignContext) writeXref() error {
	var buf bytes.Buffer

	startObj := ctx.VisualSignData.ObjectId
	count := uint32(4) // visual sig, catalog, info, signature

	buf.WriteString("xref\n")
	buf.WriteString(fmt.Sprintf("%d %d\n", startObj, count))

	// Visual signature object
	offset := ctx.Filesize
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offset))

	// Catalog object
	offset += ctx.VisualSignData.Length
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offset))

	// Info object
	offset += ctx.CatalogData.Length
	// Signature object comes after info, but info is written before sig
	infoOffset := offset + ctx.InfoData.Length
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offset))

	// Signature object
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", infoOffset))

	_, err := ctx.OutputBuffer.Write(buf.Bytes())
	return err
}

// writeTrailer writes the PDF trailer.
func (ctx *SignContext) writeTrailer() error {
	var buf bytes.Buffer

	prevXref := ctx.PDFReader.XrefInformation.StartPos

	buf.WriteString("trailer\n")
	buf.WriteString("<< /Size " + strconv.Itoa(int(ctx.SignData.ObjectId)+1))
	buf.WriteString(" /Root " + strconv.Itoa(int(ctx.CatalogData.ObjectId)) + " 0 R")
	buf.WriteString(" /Info " + strconv.Itoa(int(ctx.InfoData.ObjectId)) + " 0 R")
	buf.WriteString(" /Prev " + strconv.FormatInt(prevXref, 10))
	buf.WriteString(" >>")
	buf.WriteString("\nstartxref\n")
	buf.WriteString(strconv.FormatInt(ctx.NewXrefStart, 10))
	buf.WriteString("\n%%EOF\n")

	_, err := ctx.OutputBuffer.Write(buf.Bytes())
	return err
}

// updateByteRange calculates and writes the actual byte range values.
func (ctx *SignContext) updateByteRange() error {
	fileContent := ctx.OutputBuffer.Bytes()
	fileLen := int64(len(fileContent))

	// ByteRange[0] = 0 (start of file)
	// ByteRange[1] = position before Contents< value
	// ByteRange[2] = position after >
	// ByteRange[3] = remaining bytes

	contentsStart := ctx.SignatureContentsStartByte
	contentsEnd := contentsStart + int64(ctx.SignatureMaxLength)

	ctx.ByteRangeValues = []int64{
		0,
		contentsStart,
		contentsEnd + 1, // +1 for the > character
		fileLen - (contentsEnd + 1),
	}

	// Replace the placeholder byte range
	byteRangeStr := fmt.Sprintf("/ByteRange[0 %010d %010d %010d]",
		ctx.ByteRangeValues[1],
		ctx.ByteRangeValues[2],
		ctx.ByteRangeValues[3],
	)

	// Ensure same length as placeholder
	for len(byteRangeStr) < len(signatureByteRangePlaceholder) {
		byteRangeStr += " "
	}

	copy(fileContent[ctx.ByteRangeStartByte:ctx.ByteRangeStartByte+int64(len(signatureByteRangePlaceholder))],
		[]byte(byteRangeStr))

	return nil
}
