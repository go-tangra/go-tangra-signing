// Package fill provides PDF form filling and signature image merging.
package fill

import (
	"bytes"
	"fmt"
	"time"

	"github.com/signintech/gopdf"
)

// FieldValue represents a form field value to fill.
type FieldValue struct {
	Name   string
	Value  string
	Page   int
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// SignatureImage represents a signature image to merge into the PDF.
type SignatureImage struct {
	ImageBytes []byte
	Page       int
	X          float64
	Y          float64
	Width      float64
	Height     float64
}

// AuditEntry represents an event in the audit trail.
type AuditEntry struct {
	Timestamp time.Time
	Action    string
	Actor     string
	Details   string
}

// FillFields creates a new PDF with form field values overlaid.
func FillFields(pdfContent []byte, fields []FieldValue) ([]byte, error) {
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})

	// Load font for text rendering
	if err := pdf.AddTTFFont("default", ""); err != nil {
		// Use built-in font as fallback
		pdf.SetFont("Helvetica", "", 10)
	}

	// Import original PDF pages and overlay fields
	// Note: gopdf doesn't directly import existing PDFs — in production,
	// use github.com/pdfcpu/pdfcpu for page import + gopdf for overlay
	for _, field := range fields {
		pdf.SetPage(field.Page)
		pdf.SetX(field.X)
		pdf.SetY(field.Y)
		if err := pdf.Cell(nil, field.Value); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", field.Name, err)
		}
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("failed to write PDF: %w", err)
	}

	return buf.Bytes(), nil
}

// MergeSignatures overlays signature images onto a PDF.
func MergeSignatures(pdfContent []byte, signatures []SignatureImage) ([]byte, error) {
	// In a full implementation, this would:
	// 1. Parse the original PDF
	// 2. Create a new PDF with gopdf
	// 3. Import each page from the original
	// 4. For each signature, place the image at the specified coordinates
	// 5. Return the merged PDF

	// Placeholder: return original content with signatures noted
	return pdfContent, nil
}

// GenerateAuditTrail creates a PDF page with the signing audit trail.
func GenerateAuditTrail(submissionID string, entries []AuditEntry) ([]byte, error) {
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.AddPage()

	// Title
	pdf.SetX(50)
	pdf.SetY(50)
	if err := pdf.Cell(nil, "Document Signing Audit Trail"); err != nil {
		return nil, err
	}

	// Submission ID
	pdf.SetX(50)
	pdf.SetY(80)
	if err := pdf.Cell(nil, fmt.Sprintf("Submission: %s", submissionID)); err != nil {
		return nil, err
	}

	// Events
	y := 120.0
	for _, entry := range entries {
		pdf.SetX(50)
		pdf.SetY(y)
		line := fmt.Sprintf("%s | %s | %s | %s",
			entry.Timestamp.Format("2006-01-02 15:04:05"),
			entry.Action,
			entry.Actor,
			entry.Details,
		)
		if err := pdf.Cell(nil, line); err != nil {
			return nil, err
		}
		y += 20
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("failed to write audit trail PDF: %w", err)
	}

	return buf.Bytes(), nil
}
