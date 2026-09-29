// Package audittrail renders the audit-trail PDF of a completed submission
// (FR-043): the document's identity and hashes (original and final), every
// signer (name, e-mail, method, certificate, IP address, user agent, time)
// and the chronological event list. It is written with gopdf and the
// embedded DejaVu Sans (Latin and Cyrillic); the caller signs it with the
// tenant's system certificate. Field values never appear in it.
package audittrail

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/signintech/gopdf"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/fonts"
)

// Signer is one signer row.
type Signer struct {
	Name, Email, Party, Status string
	Method                     string
	CertSubject, CertSerial    string
	CertIssuer                 string
	IP, UserAgent              string
	SignedAt, DeclinedAt       *time.Time
	DeclineReason              string
}

// Event is one history entry.
type Event struct {
	At     time.Time
	Type   string
	Actor  string // display name or id; "" for the system
	Signer string // signer name when the event concerns one
}

// Input is the submission's record.
type Input struct {
	SubmissionID   string
	Name           string
	TemplateName   string
	Sender         string
	CreatedAt      time.Time
	CompletedAt    time.Time
	OriginalSHA256 string
	FinalSHA256    string
	FinalVersion   int
	Signers        []Signer
	Events         []Event
	GeneratedAt    time.Time
}

// ErrInput is returned for an input without an identity.
var ErrInput = errors.New("audittrail: submission id and name are required")

const (
	margin   = 48.0
	pageW    = 595.28
	pageH    = 841.89
	bodySize = 9.5
	lineGap  = 3.0
)

type writer struct {
	pdf *gopdf.GoPdf
	y   float64
	err error
}

func (w *writer) font(size float64) {
	if w.err == nil {
		w.err = w.pdf.SetFont("dejavu", "", size)
	}
}

func (w *writer) newPage() {
	w.pdf.AddPage()
	w.y = margin
}

func (w *writer) ensure(h float64) {
	if w.y+h > pageH-margin {
		w.newPage()
	}
}

// line writes text wrapped to the width at x, advancing y.
func (w *writer) text(x float64, s string, size float64) {
	if w.err != nil {
		return
	}
	w.font(size)
	lines, err := w.pdf.SplitTextWithWordWrap(sanitize(s), pageW-margin-x)
	if err != nil {
		lines = []string{sanitize(s)}
	}
	for _, l := range lines {
		w.ensure(size + lineGap)
		w.pdf.SetXY(x, w.y)
		if err := w.pdf.Text(l); err != nil && w.err == nil {
			w.err = err
		}
		w.y += size + lineGap
	}
}

// pair writes "label: value" with the label in grey.
func (w *writer) pair(label, value string) {
	if value == "" {
		return
	}
	w.pdf.SetTextColor(0x60, 0x66, 0x70)
	w.font(bodySize)
	w.ensure(bodySize + lineGap)
	w.pdf.SetXY(margin, w.y)
	if err := w.pdf.Text(label); err != nil && w.err == nil {
		w.err = err
	}
	w.pdf.SetTextColor(0x10, 0x18, 0x3a)
	w.text(margin+120, value, bodySize)
}

func (w *writer) heading(s string) {
	w.y += 8
	w.ensure(40)
	w.pdf.SetTextColor(0x10, 0x18, 0x3a)
	w.text(margin, s, 13)
	w.pdf.SetStrokeColor(0xc8, 0xcc, 0xd4)
	w.pdf.SetLineWidth(0.6)
	w.pdf.Line(margin, w.y, pageW-margin, w.y)
	w.y += 6
}

// sanitize drops control characters (a PDF text show cannot carry them).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }

func stampPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return stamp(*t)
}

// Build renders the audit trail.
func Build(in Input) ([]byte, error) {
	if in.SubmissionID == "" || strings.TrimSpace(in.Name) == "" {
		return nil, ErrInput
	}
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.SetInfo(gopdf.PdfInfo{Title: "Audit trail — " + sanitize(in.Name), Creator: "Tangra Signing", Producer: "Tangra Signing",
		CreationDate: in.GeneratedAt})
	if err := pdf.AddTTFFontData("dejavu", fonts.DejaVuSans); err != nil {
		return nil, err
	}
	w := &writer{pdf: pdf}
	w.newPage()

	w.pdf.SetTextColor(0x10, 0x18, 0x3a)
	w.text(margin, "Audit trail / Одитна следа", 18)
	w.y += 4
	w.text(margin, in.Name, 12)
	w.y += 6

	w.heading("Document")
	w.pair("Submission", in.SubmissionID)
	w.pair("Template", in.TemplateName)
	w.pair("Sender", in.Sender)
	w.pair("Created", stamp(in.CreatedAt))
	w.pair("Completed", stamp(in.CompletedAt))
	w.pair("Original SHA-256", in.OriginalSHA256)
	w.pair("Final SHA-256", in.FinalSHA256)
	w.pair("Final version", fmt.Sprintf("%d", in.FinalVersion))

	w.heading(fmt.Sprintf("Signers (%d)", len(in.Signers)))
	for i, s := range in.Signers {
		w.ensure(90)
		w.text(margin, fmt.Sprintf("%d. %s", i+1, s.Name), 11)
		w.pair("E-mail", s.Email)
		w.pair("Party", s.Party)
		w.pair("Status", s.Status)
		w.pair("Method", methodName(s.Method))
		w.pair("Certificate", s.CertSubject)
		w.pair("Serial", s.CertSerial)
		w.pair("Issuer", s.CertIssuer)
		w.pair("Signed", stampPtr(s.SignedAt))
		w.pair("Declined", stampPtr(s.DeclinedAt))
		w.pair("Reason", s.DeclineReason)
		w.pair("IP address", s.IP)
		w.pair("User agent", s.UserAgent)
		w.y += 6
	}

	w.heading(fmt.Sprintf("Events (%d)", len(in.Events)))
	for _, e := range in.Events {
		who := e.Actor
		if e.Signer != "" && e.Signer != who {
			if who != "" {
				who += " → "
			}
			who += e.Signer
		}
		line := stamp(e.At) + "   " + e.Type
		if who != "" {
			line += "   " + who
		}
		w.text(margin, line, bodySize)
	}

	w.y += 12
	w.pdf.SetTextColor(0x60, 0x66, 0x70)
	w.text(margin, "Generated "+stamp(in.GeneratedAt)+". This document is signed with the tenant's system certificate; "+
		"a change to it invalidates the signature.", 8)
	if w.err != nil {
		return nil, w.err
	}
	return pdf.GetBytesPdfReturnErr()
}

func methodName(m string) string {
	switch m {
	case "local_certificate":
		return "Personal signing certificate (PIN)"
	case "qes":
		return "Qualified electronic signature (card)"
	default:
		return m
	}
}
