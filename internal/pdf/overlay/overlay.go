// Package overlay turns one signer's fields and values into what the PDF
// receives when they sign (research D2): value images placed by the
// incremental writer, and the appearance of the CMS signature itself on the
// signer's first signature field. Other signature fields show the same
// appearance as stamps. Empty values draw nothing; file fields are stored,
// not drawn.
package overlay

import (
	"errors"
	"image"
	"strings"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// ErrPage is returned for a field on a page the document does not have.
var ErrPage = errors.New("overlay: field page out of range")

// Input is everything drawn for one signature.
type Input struct {
	Pages     []incr.Page
	Fields    []store.Field     // the signer's visible fields
	Values    map[string]string // field id → value
	Uploads   map[string][]byte // field id → PNG/JPEG (image, signature, initials, stamp)
	Signature []byte            // the drawn or typed signature (PNG/JPEG), optional
	Name      string            // signer's display name
	Date      string            // formatted signing time
	Issuer    string            // certificate issuer CN
}

// Result is the stamps for the increment and the visible signature.
type Result struct {
	Stamps     []incr.Stamp
	Appearance *sign.Visible
}

// Build renders the input.
func Build(in Input) (Result, error) {
	var out Result
	for _, f := range in.Fields {
		if f.Page < 1 || f.Page > len(in.Pages) {
			return Result{}, ErrPage
		}
		page := in.Pages[f.Page-1]
		w, h := page.DisplayBoxSize(f.W, f.H)
		img, err := draw(in, f, w, h)
		if err != nil {
			return Result{}, err
		}
		if img == nil {
			continue
		}
		if f.Type == "signature" && out.Appearance == nil {
			png, err := render.PNG(render.Rotate(img, page.Rotate))
			if err != nil {
				return Result{}, err
			}
			out.Appearance = &sign.Visible{Page: f.Page, Rect: page.Rect(f.X, f.Y, f.W, f.H), Image: png}
			continue
		}
		out.Stamps = append(out.Stamps, incr.Stamp{Page: f.Page, X: f.X, Y: f.Y, W: f.W, H: f.H, Image: img, Name: f.Name})
	}
	return out, nil
}

func draw(in Input, f store.Field, w, h float64) (image.Image, error) {
	v := strings.TrimSpace(in.Values[f.ID])
	upload := in.Uploads[f.ID]
	switch f.Type {
	case "text", "number", "date", "select", "radio":
		if v == "" {
			return nil, nil
		}
		return render.Text(v, w, h, f.FontSize)
	case "cells":
		if v == "" {
			return nil, nil
		}
		return render.Cells(v, w, h, f.FontSize)
	case "checkbox":
		if v != "true" {
			return nil, nil
		}
		return render.Check(w, h)
	case "image":
		if len(upload) == 0 {
			return nil, nil
		}
		return render.Picture(upload, w, h)
	case "signature":
		sig := upload
		if len(sig) == 0 {
			sig = in.Signature
		}
		return render.Stamp{Signature: sig, Name: in.Name, Date: in.Date, Issuer: in.Issuer}.Render(w, h)
	case "initials":
		if len(upload) > 0 {
			return render.Picture(upload, w, h)
		}
		return render.Typed(Initials(in.Name), w, h)
	case "stamp":
		if len(upload) > 0 {
			return render.Picture(upload, w, h)
		}
		return render.Stamp{Name: in.Name, Date: in.Date, Issuer: in.Issuer}.Render(w, h)
	default: // file: stored with the submission, nothing to draw
		return nil, nil
	}
}

// Initials returns the first letter of each word of name ("Мария Иванова" → "М.И.").
func Initials(name string) string {
	var b strings.Builder
	for _, w := range strings.Fields(name) {
		r := []rune(w)
		b.WriteRune(r[0])
		b.WriteByte('.')
	}
	return b.String()
}
