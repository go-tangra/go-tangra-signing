// Package incr places a signer's field values on a PDF as an incremental
// update (research D2): each value is an image-appearance annotation added to
// its page, written after the existing bytes (pdfcpu's increment writer), so
// every earlier signature's byte range stays intact and valid. It also maps
// the builder's display geometry (page fractions, top-left origin, the page
// as a viewer shows it after /Rotate) to PDF user space.
package incr

import (
	"bytes"
	"errors"
	"fmt"
	"image"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/limits"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
)

// Errors.
var (
	ErrPage    = errors.New("incr: page out of range")
	ErrVersion = errors.New("incr: PDF version below 1.4 cannot take incremental updates")
)

// Page is one page's geometry: the visible box (CropBox, else MediaBox) in
// user space and the /Rotate of the page (0, 90, 180, 270).
type Page struct {
	X0, Y0, X1, Y1 float64
	Rotate         int
}

// Width and Height are the user-space box size.
func (p Page) Width() float64  { return p.X1 - p.X0 }
func (p Page) Height() float64 { return p.Y1 - p.Y0 }

// DisplaySize is the page size as a viewer shows it (swapped at 90/270).
func (p Page) DisplaySize() (w, h float64) {
	if p.Rotate == 90 || p.Rotate == 270 {
		return p.Height(), p.Width()
	}
	return p.Width(), p.Height()
}

// Rect maps a display box (fractions, top-left origin) to a user-space
// rectangle [llx lly urx ury].
func (p Page) Rect(fx, fy, fw, fh float64) [4]float64 {
	W, H := p.Width(), p.Height()
	switch p.Rotate {
	case 90: // display top-left is user bottom-left; right = up, down = right
		return [4]float64{p.X0 + fy*W, p.Y0 + fx*H, p.X0 + (fy+fh)*W, p.Y0 + (fx+fw)*H}
	case 180: // display top-left is user bottom-right
		return [4]float64{p.X1 - (fx+fw)*W, p.Y0 + fy*H, p.X1 - fx*W, p.Y0 + (fy+fh)*H}
	case 270: // display top-left is user top-right
		return [4]float64{p.X1 - (fy+fh)*W, p.Y1 - (fx+fw)*H, p.X1 - fy*W, p.Y1 - fx*H}
	default:
		return [4]float64{p.X0 + fx*W, p.Y1 - (fy+fh)*H, p.X0 + (fx+fw)*W, p.Y1 - fy*H}
	}
}

// DisplayBoxSize is the size in points of a display box on this page (what
// the value image is rendered at, before rotation).
func (p Page) DisplayBoxSize(fw, fh float64) (w, h float64) {
	dw, dh := p.DisplaySize()
	return fw * dw, fh * dh
}

func normRotate(r int) int { return ((r % 360) + 360) % 360 }

func read(pdf []byte) (*model.Context, error) {
	ctx, err := api.ReadContext(bytes.NewReader(pdf), limits.Config())
	if err != nil {
		return nil, err
	}
	if err := ctx.EnsurePageCount(); err != nil {
		return nil, err
	}
	return ctx, nil
}

func pageOf(ctx *model.Context, n int) (types.Dict, *types.IndirectRef, Page, error) {
	if n < 1 || n > ctx.PageCount {
		return nil, nil, Page{}, ErrPage
	}
	d, ref, inh, err := ctx.PageDict(n, false)
	if err != nil {
		return nil, nil, Page{}, err
	}
	if d == nil || ref == nil || inh == nil || inh.MediaBox == nil {
		return nil, nil, Page{}, fmt.Errorf("incr: page %d has no media box", n)
	}
	box := inh.MediaBox
	if inh.CropBox != nil {
		box = inh.CropBox
	}
	return d, ref, Page{X0: box.LL.X, Y0: box.LL.Y, X1: box.UR.X, Y1: box.UR.Y, Rotate: normRotate(inh.Rotate)}, nil
}

// Pages returns the geometry of every page.
func Pages(pdf []byte) ([]Page, error) {
	ctx, err := read(pdf)
	if err != nil {
		return nil, err
	}
	out := make([]Page, 0, ctx.PageCount)
	for n := 1; n <= ctx.PageCount; n++ {
		_, _, p, err := pageOf(ctx, n)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Stamp is one value to place: a display box on a page and its image in
// display orientation (it is rotated for the page here).
type Stamp struct {
	Page       int
	X, Y, W, H float64
	Image      image.Image
	Name       string // field name (annotation /Contents, never the value)
}

// annotation flags: print + locked (the value cannot be moved or deleted).
const annotFlags = 4 | 128

// AddStamps returns pdf with the stamps appended as one incremental update.
// No stamps returns pdf unchanged.
func AddStamps(pdf []byte, stamps []Stamp) ([]byte, error) {
	if len(stamps) == 0 {
		return pdf, nil
	}
	ctx, err := read(pdf)
	if err != nil {
		return nil, err
	}
	if ctx.HeaderVersion != nil && *ctx.HeaderVersion < model.V14 {
		return nil, ErrVersion
	}
	// Keep the file's own cross-reference format: readers (digitorus/pdf,
	// used by the signer) cannot follow an xref stream whose /Prev is a
	// classic table.
	ctx.Configuration.WriteObjectStream = false
	ctx.Configuration.WriteXRefStream = ctx.Read.UsingXRefStreams
	ctx.Write.Increment = true
	ctx.Write.Offset = ctx.Read.FileSize
	before := *ctx.XRefTable.Size
	for i, s := range stamps {
		if err := addStamp(ctx, s, i); err != nil {
			return nil, err
		}
	}
	for n := before; n < *ctx.XRefTable.Size; n++ {
		ctx.Write.IncrementWithObjNr(n)
	}
	var out bytes.Buffer
	out.Grow(len(pdf) + 64<<10)
	out.Write(pdf)
	if err := api.WriteIncrement(ctx, &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func addStamp(ctx *model.Context, s Stamp, i int) error {
	pageDict, pageRef, page, err := pageOf(ctx, s.Page)
	if err != nil {
		return err
	}
	rect := page.Rect(s.X, s.Y, s.W, s.H)
	rw, rh := rect[2]-rect[0], rect[3]-rect[1]
	png, err := render.PNG(render.Rotate(s.Image, page.Rotate))
	if err != nil {
		return err
	}
	imgRef, _, _, err := model.CreateImageResource(ctx.XRefTable, bytes.NewReader(png))
	if err != nil {
		return err
	}
	content := fmt.Sprintf("q %.4f 0 0 %.4f 0 0 cm /Im0 Do Q", rw, rh)
	form, err := ctx.XRefTable.NewStreamDictForBuf([]byte(content))
	if err != nil {
		return err
	}
	form.InsertName("Type", "XObject")
	form.InsertName("Subtype", "Form")
	form.Insert("BBox", types.NewNumberArray(0, 0, rw, rh))
	form.Insert("Resources", types.Dict{"XObject": types.Dict{"Im0": *imgRef}})
	if err := form.Encode(); err != nil {
		return err
	}
	formRef, err := ctx.XRefTable.IndRefForNewObject(*form)
	if err != nil {
		return err
	}
	annot := types.Dict{
		"Type":     types.Name("Annot"),
		"Subtype":  types.Name("Stamp"),
		"Rect":     types.NewNumberArray(rect[0], rect[1], rect[2], rect[3]),
		"F":        types.Integer(annotFlags),
		"P":        *pageRef,
		"NM":       types.StringLiteral(fmt.Sprintf("tangra-value-%d-%d", s.Page, i)),
		"AP":       types.Dict{"N": *formRef},
		"Contents": types.StringLiteral(types.EncodeUTF16String(s.Name)),
	}
	annotRef, err := ctx.XRefTable.IndRefForNewObject(annot)
	if err != nil {
		return err
	}
	return appendAnnot(ctx, pageDict, pageRef, *annotRef)
}

// appendAnnot adds ref to the page's /Annots (a direct array, an indirect
// array, or absent), marking the changed object for the increment.
func appendAnnot(ctx *model.Context, pageDict types.Dict, pageRef *types.IndirectRef, ref types.IndirectRef) error {
	obj, found := pageDict.Find("Annots")
	if !found {
		pageDict.Insert("Annots", types.Array{ref})
		ctx.Write.IncrementWithObjNr(pageRef.ObjectNumber.Value())
		return nil
	}
	if ir, ok := obj.(types.IndirectRef); ok {
		arr, err := ctx.DereferenceArray(ir)
		if err != nil {
			return err
		}
		entry, ok := ctx.FindTableEntryForIndRef(&ir)
		if !ok {
			return fmt.Errorf("incr: annotations array %d not found", ir.ObjectNumber.Value())
		}
		entry.Object = append(arr, ref)
		ctx.Write.IncrementWithObjNr(ir.ObjectNumber.Value())
		return nil
	}
	arr, ok := obj.(types.Array)
	if !ok {
		return fmt.Errorf("incr: malformed /Annots")
	}
	pageDict.Update("Annots", append(arr, ref))
	ctx.Write.IncrementWithObjNr(pageRef.ObjectNumber.Value())
	return nil
}
