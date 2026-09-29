// Package render draws field values as images for the PDF: text, numbers,
// dates and choices with DejaVu Sans (Latin and Cyrillic without embedding a
// PDF font), check marks, spaced "cells", uploaded signature/image values
// scaled into their box, the visible signature appearance (signature image
// with a "Digitally signed by" caption, as v3) and a typed signature in
// Great Vibes. Images are rendered at Scale pixels per point and rotated to
// counter a page's /Rotate so they show upright.
package render

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // uploaded signatures and images
	"image/png"
	"math"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/fonts"
)

// Scale is the rendering resolution in pixels per PDF point.
const Scale = 3.0

// MaxSide bounds a rendered image side in pixels.
const MaxSide = 2400

// MaxImagePixels bounds a decoded upload (decompression-bomb guard).
const MaxImagePixels = 25_000_000

// Errors.
var (
	ErrImage = errors.New("render: not a usable PNG or JPEG image")
	ErrBox   = errors.New("render: field box too small")
)

var (
	parseOnce  sync.Once
	sansFont   *opentype.Font
	scriptFont *opentype.Font
	parseErr   error
)

func loadFonts() error {
	parseOnce.Do(func() {
		if sansFont, parseErr = opentype.Parse(fonts.DejaVuSans); parseErr != nil {
			return
		}
		scriptFont, parseErr = opentype.Parse(fonts.GreatVibes)
	})
	return parseErr
}

func face(f *opentype.Font, px float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
}

// canvas is a transparent RGBA image for a box of w×h points.
func canvas(wPt, hPt float64) (*image.RGBA, error) {
	w, h := int(math.Round(wPt*Scale)), int(math.Round(hPt*Scale))
	if w < 3 || h < 3 {
		return nil, ErrBox
	}
	if w > MaxSide {
		h = h * MaxSide / w
		w = MaxSide
	}
	if h > MaxSide {
		w = w * MaxSide / h
		h = MaxSide
	}
	if w < 3 || h < 3 {
		return nil, ErrBox
	}
	return image.NewRGBA(image.Rect(0, 0, w, h)), nil
}

var ink = image.NewUniform(color.RGBA{R: 0x10, G: 0x18, B: 0x3a, A: 0xff})

// fitFace returns the largest face (≤ wanted px) whose rendering of s fits
// maxW×maxH pixels.
func fitFace(f *opentype.Font, s string, wantPx float64, maxW, maxH int) (font.Face, error) {
	px := wantPx
	if px <= 0 || px > float64(maxH)*0.8 {
		px = float64(maxH) * 0.8
	}
	for ; px >= 4; px *= 0.9 {
		fc, err := face(f, px)
		if err != nil {
			return nil, err
		}
		if font.MeasureString(fc, s).Ceil() <= maxW {
			return fc, nil
		}
		_ = fc.Close()
	}
	return face(f, 4)
}

func drawLine(img *image.RGBA, fc font.Face, s string, x int, centerY int) {
	m := fc.Metrics()
	base := centerY + (m.Ascent.Ceil()-m.Descent.Ceil())/2
	d := &font.Drawer{Dst: img, Src: ink, Face: fc, Dot: fixed.P(x, base)}
	d.DrawString(s)
}

// Text renders one line of text left-aligned and vertically centred in a
// w×h point box; fontSize (points, 0 = fit) shrinks to fit the width.
func Text(s string, wPt, hPt, fontSize float64) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	img, err := canvas(wPt, hPt)
	if err != nil {
		return nil, err
	}
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return img, nil
	}
	b := img.Bounds()
	pad := int(2 * Scale)
	fc, err := fitFace(sansFont, s, fontSize*Scale, b.Dx()-2*pad, b.Dy())
	if err != nil {
		return nil, err
	}
	defer func() { _ = fc.Close() }()
	drawLine(img, fc, s, pad, b.Dy()/2)
	return img, nil
}

// Cells renders the characters of s spread evenly across the box (comb
// fields such as IBAN or national-id boxes).
func Cells(s string, wPt, hPt, fontSize float64) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	img, err := canvas(wPt, hPt)
	if err != nil {
		return nil, err
	}
	runes := []rune(strings.ReplaceAll(s, " ", ""))
	if len(runes) == 0 {
		return img, nil
	}
	b := img.Bounds()
	cell := b.Dx() / len(runes)
	if cell < 2 {
		return nil, ErrBox
	}
	fc, err := fitFace(sansFont, "W", fontSize*Scale, cell, b.Dy())
	if err != nil {
		return nil, err
	}
	defer func() { _ = fc.Close() }()
	for i, r := range runes {
		w := font.MeasureString(fc, string(r)).Ceil()
		drawLine(img, fc, string(r), i*cell+(cell-w)/2, b.Dy()/2)
	}
	return img, nil
}

// Check renders a check mark filling the box.
func Check(wPt, hPt float64) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	img, err := canvas(wPt, hPt)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	fc, err := face(sansFont, float64(side)*0.9)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fc.Close() }()
	w := font.MeasureString(fc, "✓").Ceil()
	drawLine(img, fc, "✓", (b.Dx()-w)/2, b.Dy()/2)
	return img, nil
}

// DecodeImage decodes an uploaded PNG/JPEG, refusing oversized dimensions
// before decoding the pixels.
func DecodeImage(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxImagePixels {
		return nil, ErrImage
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrImage
	}
	return img, nil
}

// fitInto scales src into dst's bounds preserving the aspect ratio, centred.
func fitInto(dst *image.RGBA, src image.Image, area image.Rectangle) {
	sb := src.Bounds()
	scale := math.Min(float64(area.Dx())/float64(sb.Dx()), float64(area.Dy())/float64(sb.Dy()))
	w, h := int(float64(sb.Dx())*scale), int(float64(sb.Dy())*scale)
	x0 := area.Min.X + (area.Dx()-w)/2
	y0 := area.Min.Y + (area.Dy()-h)/2
	xdraw.CatmullRom.Scale(dst, image.Rect(x0, y0, x0+w, y0+h), src, sb, draw.Over, nil)
}

// Picture renders an uploaded image scaled into the box.
func Picture(data []byte, wPt, hPt float64) (image.Image, error) {
	src, err := DecodeImage(data)
	if err != nil {
		return nil, err
	}
	img, err := canvas(wPt, hPt)
	if err != nil {
		return nil, err
	}
	fitInto(img, src, img.Bounds())
	return img, nil
}

// Typed renders a typed signature in the script font.
func Typed(name string, wPt, hPt float64) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	img, err := canvas(wPt, hPt)
	if err != nil {
		return nil, err
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return img, nil
	}
	b := img.Bounds()
	fc, err := fitFace(scriptFont, name, float64(b.Dy())*0.75, b.Dx()-int(4*Scale), b.Dy())
	if err != nil {
		return nil, err
	}
	defer func() { _ = fc.Close() }()
	drawLine(img, fc, name, int(2*Scale), b.Dy()/2)
	return img, nil
}

// Stamp is the visible signature appearance: the signature image (or the
// typed name) above a caption with the signer, date and issuer.
type Stamp struct {
	Signature []byte // PNG/JPEG of the drawn signature (optional)
	Name      string
	Date      string // already formatted
	Issuer    string
}

// Render draws the stamp into a w×h point box.
func (s Stamp) Render(wPt, hPt float64) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	img, err := canvas(wPt, hPt)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	captionH := b.Dy() * 2 / 5
	sigArea := image.Rect(0, 0, b.Dx(), b.Dy()-captionH)
	if len(s.Signature) > 0 {
		src, err := DecodeImage(s.Signature)
		if err != nil {
			return nil, err
		}
		fitInto(img, src, sigArea)
	} else if s.Name != "" {
		fc, err := fitFace(scriptFont, s.Name, float64(sigArea.Dy())*0.8, sigArea.Dx(), sigArea.Dy())
		if err != nil {
			return nil, err
		}
		drawLine(img, fc, s.Name, 0, sigArea.Dy()/2)
		_ = fc.Close()
	}
	lines := []string{"Digitally signed by " + s.Name, s.Date}
	if s.Issuer != "" {
		lines = append(lines, s.Issuer)
	}
	lineH := captionH / len(lines)
	longest := ""
	for _, l := range lines {
		if len(l) > len(longest) {
			longest = l
		}
	}
	fc, err := fitFace(sansFont, longest, float64(lineH)*0.8, b.Dx(), lineH)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fc.Close() }()
	for i, l := range lines {
		drawLine(img, fc, l, 0, b.Dy()-captionH+i*lineH+lineH/2)
	}
	return img, nil
}

// Rotate turns img by -rotate degrees (the page's /Rotate, a multiple of 90)
// so that it shows upright on the rotated page.
func Rotate(img image.Image, rotate int) image.Image {
	r := ((rotate % 360) + 360) % 360
	if r == 0 {
		return img
	}
	b := img.Bounds()
	var out *image.RGBA
	if r == 180 {
		out = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	} else {
		out = image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px, py := x-b.Min.X, y-b.Min.Y
			var nx, ny int
			switch r {
			case 90: // page rotated clockwise: turn the image counter-clockwise
				nx, ny = py, b.Dx()-1-px
			case 180:
				nx, ny = b.Dx()-1-px, b.Dy()-1-py
			case 270:
				nx, ny = b.Dy()-1-py, px
			default:
				return img
			}
			out.Set(nx, ny, img.At(x, y))
		}
	}
	return out
}

// PNG encodes img.
func PNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
