package incr

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/limits"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func eqRect(t *testing.T, got, want [4]float64, what string) {
	t.Helper()
	for i := range got {
		if !near(got[i], want[i]) {
			t.Fatalf("%s: rect %v, want %v", what, got, want)
		}
	}
}

func TestRectAllRotations(t *testing.T) {
	// A 600×800 page with its box at (10,20); display box at the top-left
	// quarter-width, 10 % high, starting 10 %/5 % in.
	fx, fy, fw, fh := 0.1, 0.05, 0.25, 0.1
	p := Page{X0: 10, Y0: 20, X1: 610, Y1: 820}
	eqRect(t, p.Rect(fx, fy, fw, fh), [4]float64{10 + 60, 820 - 0.15*800, 10 + 0.35*600, 820 - 40}, "rotate 0")
	p.Rotate = 90
	eqRect(t, p.Rect(fx, fy, fw, fh), [4]float64{10 + 0.05*600, 20 + 0.1*800, 10 + 0.15*600, 20 + 0.35*800}, "rotate 90")
	p.Rotate = 180
	eqRect(t, p.Rect(fx, fy, fw, fh), [4]float64{610 - 0.35*600, 20 + 0.05*800, 610 - 60, 20 + 0.15*800}, "rotate 180")
	p.Rotate = 270
	eqRect(t, p.Rect(fx, fy, fw, fh), [4]float64{610 - 0.15*600, 820 - 0.35*800, 610 - 0.05*600, 820 - 0.1*800}, "rotate 270")
	if w, h := p.DisplaySize(); w != 800 || h != 600 {
		t.Fatalf("display size at 270 = %v×%v", w, h)
	}
	if w, h := p.DisplayBoxSize(0.5, 0.5); w != 400 || h != 300 {
		t.Fatalf("display box = %v×%v", w, h)
	}
	p.Rotate = 0
	if w, h := p.DisplaySize(); w != 600 || h != 800 {
		t.Fatal("display size at 0")
	}
	if normRotate(-90) != 270 || normRotate(450) != 90 {
		t.Fatal("normRotate")
	}
}

func marker(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, h/2, color.Black)
	}
	return img
}

func annotCount(t *testing.T, pdf []byte, page int) int {
	t.Helper()
	ctx, err := read(pdf)
	if err != nil {
		t.Fatal(err)
	}
	d, _, _, err := ctx.PageDict(page, false)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := d.Find("Annots")
	if !ok {
		return 0
	}
	arr, err := ctx.DereferenceArray(o)
	if err != nil {
		t.Fatal(err)
	}
	return len(arr)
}

func TestAddStampsIsIncremental(t *testing.T) {
	src := pdftest.Contract(2)
	pages, err := Pages(src)
	if err != nil || len(pages) != 2 || pages[0].Rotate != 0 || pages[0].Width() < 500 {
		t.Fatalf("pages = %+v %v", pages, err)
	}
	if same, err := AddStamps(src, nil); err != nil || !bytes.Equal(same, src) {
		t.Fatal("no stamps must return the input")
	}
	out, err := AddStamps(src, []Stamp{
		{Page: 1, X: 0.2, Y: 0.1, W: 0.3, H: 0.03, Image: marker(90, 9), Name: "Name"},
		{Page: 1, X: 0.2, Y: 0.2, W: 0.3, H: 0.03, Image: marker(90, 9), Name: "Заплата"},
		{Page: 2, X: 0.1, Y: 0.5, W: 0.2, H: 0.05, Image: marker(60, 15), Name: "Signature"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, src) || len(out) <= len(src) {
		t.Fatal("the update must append to the original bytes")
	}
	if _, err := limits.Open(context.Background(), out, limits.Limits{}); err != nil {
		t.Fatalf("stamped PDF invalid: %v", err)
	}
	if annotCount(t, out, 1) != 2 || annotCount(t, out, 2) != 1 {
		t.Fatalf("annotations: %d / %d", annotCount(t, out, 1), annotCount(t, out, 2))
	}
	// A second signer's update appends again and keeps the first one.
	out2, err := AddStamps(out, []Stamp{{Page: 1, X: 0.6, Y: 0.8, W: 0.2, H: 0.05, Image: marker(60, 15), Name: "Employer"}})
	if err != nil || !bytes.HasPrefix(out2, out) || annotCount(t, out2, 1) != 3 {
		t.Fatalf("second update: %v", err)
	}
}

func TestAddStampsRotatedPageAndRefusals(t *testing.T) {
	var rotated bytes.Buffer
	if err := api.Rotate(bytes.NewReader(pdftest.Contract(1)), &rotated, 90, nil, limits.Config()); err != nil {
		t.Fatal(err)
	}
	pages, err := Pages(rotated.Bytes())
	if err != nil || pages[0].Rotate != 90 {
		t.Fatalf("rotated fixture: %+v %v", pages, err)
	}
	img, _ := render.Text("Rotated", 100, 20, 0)
	out, err := AddStamps(rotated.Bytes(), []Stamp{{Page: 1, X: 0.1, Y: 0.1, W: 0.3, H: 0.05, Image: img, Name: "Rotated"}})
	if err != nil || annotCount(t, out, 1) != 1 {
		t.Fatalf("rotated stamp: %v", err)
	}
	if _, err := AddStamps(pdftest.Contract(1), []Stamp{{Page: 3, W: 0.1, H: 0.1, Image: marker(3, 3)}}); !errors.Is(err, ErrPage) {
		t.Fatalf("page out of range: %v", err)
	}
	if _, err := AddStamps([]byte("not a pdf"), []Stamp{{Page: 1, Image: marker(3, 3)}}); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := Pages([]byte("not a pdf")); err == nil {
		t.Fatal("garbage pages accepted")
	}
}
