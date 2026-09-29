package render

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// inked counts non-transparent pixels.
func inked(img image.Image) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				n++
			}
		}
	}
	return n
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, h/2, color.Black)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestTextAndCells(t *testing.T) {
	img, err := Text("Мария Иванова — 5000 лв.", 200, 20, 11)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 600 || img.Bounds().Dy() != 60 || inked(img) == 0 {
		t.Fatalf("text %v inked %d", img.Bounds(), inked(img))
	}
	// A very long value still fits (shrinks) and never panics.
	if _, err := Text(string(bytes.Repeat([]byte("W"), 500)), 100, 12, 0); err != nil {
		t.Fatal(err)
	}
	blank, _ := Text("  ", 50, 10, 0)
	if inked(blank) != 0 {
		t.Fatal("blank text drew ink")
	}
	cells, err := Cells("BG80 BNBG 9661", 200, 20, 0)
	if err != nil || inked(cells) == 0 {
		t.Fatalf("cells: %v", err)
	}
	if empty, _ := Cells("", 50, 10, 0); inked(empty) != 0 {
		t.Fatal("empty cells drew ink")
	}
	if _, err := Cells("0123456789", 3, 10, 0); !errors.Is(err, ErrBox) {
		t.Fatalf("tiny cells: %v", err)
	}
	if _, err := Text("x", 0.5, 10, 0); !errors.Is(err, ErrBox) {
		t.Fatalf("tiny box: %v", err)
	}
	big, _ := Text("x", 5000, 5000, 0)
	if big.Bounds().Dx() > MaxSide || big.Bounds().Dy() > MaxSide {
		t.Fatalf("unbounded canvas %v", big.Bounds())
	}
}

func TestCheckTypedPictureStamp(t *testing.T) {
	if img, err := Check(12, 12); err != nil || inked(img) == 0 {
		t.Fatalf("check: %v", err)
	}
	if img, err := Typed("Ivan Petrov", 150, 40); err != nil || inked(img) == 0 {
		t.Fatalf("typed: %v", err)
	}
	if img, _ := Typed(" ", 150, 40); inked(img) != 0 {
		t.Fatal("blank typed drew ink")
	}
	pic, err := Picture(pngOf(t, 400, 100), 100, 50)
	if err != nil || inked(pic) == 0 {
		t.Fatalf("picture: %v", err)
	}
	if _, err := Picture([]byte("not an image"), 10, 10); !errors.Is(err, ErrImage) {
		t.Fatalf("garbage image: %v", err)
	}
	if _, err := Picture(pngOf(t, 10, 10), 0.1, 10); !errors.Is(err, ErrBox) {
		t.Fatalf("tiny picture box: %v", err)
	}
	st, err := Stamp{Signature: pngOf(t, 300, 80), Name: "Мария Иванова", Date: "2026-09-29 14:05 UTC", Issuer: "Tangra Signing CA"}.Render(180, 60)
	if err != nil || inked(st) == 0 {
		t.Fatalf("stamp: %v", err)
	}
	if _, err := (Stamp{Name: "Ivan"}).Render(180, 60); err != nil {
		t.Fatalf("stamp without image: %v", err)
	}
	if _, err := (Stamp{Signature: []byte("junk"), Name: "x"}).Render(180, 60); !errors.Is(err, ErrImage) {
		t.Fatalf("stamp with junk image: %v", err)
	}
	if _, err := (Stamp{Name: "x"}).Render(0.1, 1); !errors.Is(err, ErrBox) {
		t.Fatalf("tiny stamp: %v", err)
	}
}

func TestDecodeImageBombGuard(t *testing.T) {
	// A PNG header claiming 100k×100k pixels is refused before decoding.
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := b.Bytes()
	// IHDR width/height live at bytes 16..24.
	data[16], data[17], data[18], data[19] = 0x00, 0x01, 0x86, 0xa0
	data[20], data[21], data[22], data[23] = 0x00, 0x01, 0x86, 0xa0
	if _, err := DecodeImage(data); !errors.Is(err, ErrImage) {
		t.Fatalf("bomb accepted: %v", err)
	}
}

func TestRotateAndPNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	src.Set(0, 0, color.Black) // top-left marker
	if Rotate(src, 0) != src || Rotate(src, 360) != src {
		t.Fatal("no-op rotation copied")
	}
	for rot, want := range map[int]image.Point{90: {0, 3}, 180: {3, 1}, 270: {1, 0}} {
		out := Rotate(src, rot)
		b := out.Bounds()
		if (rot == 180 && (b.Dx() != 4 || b.Dy() != 2)) || (rot != 180 && (b.Dx() != 2 || b.Dy() != 4)) {
			t.Fatalf("rot %d bounds %v", rot, b)
		}
		if _, _, _, a := out.At(want.X, want.Y).RGBA(); a == 0 {
			t.Errorf("rot %d: marker not at %v", rot, want)
		}
	}
	if Rotate(src, 45) != src {
		t.Fatal("non-right-angle rotation must be a no-op")
	}
	b, err := PNG(src)
	if err != nil || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatalf("png: %v", err)
	}
}
