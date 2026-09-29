package overlay

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, h/2, color.Black)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func field(id, typ string, page int, y float64) store.Field {
	return store.Field{ID: id, Name: id, Type: typ, Party: "p1", Page: page, X: 0.1, Y: y, W: 0.3, H: 0.04}
}

func TestBuildEveryType(t *testing.T) {
	doc := pdftest.Contract(2)
	pages, err := incr.Pages(doc)
	if err != nil {
		t.Fatal(err)
	}
	fields := []store.Field{
		field("name", "text", 1, 0.10), field("salary", "number", 1, 0.15), field("date", "date", 1, 0.20),
		field("city", "select", 1, 0.25), field("kind", "radio", 1, 0.30), field("iban", "cells", 1, 0.35),
		field("agree", "checkbox", 1, 0.40), field("off", "checkbox", 1, 0.45), field("photo", "image", 1, 0.50),
		field("sig", "signature", 1, 0.80), field("sig2", "signature", 2, 0.80), field("ini", "initials", 2, 0.10),
		field("ini2", "initials", 2, 0.15), field("seal", "stamp", 2, 0.20), field("seal2", "stamp", 2, 0.25),
		field("attach", "file", 2, 0.30), field("empty", "text", 2, 0.35), field("nopic", "image", 2, 0.40),
		field("nocells", "cells", 2, 0.45),
	}
	in := Input{
		Pages: pages, Fields: fields,
		Values: map[string]string{
			"name": "Мария Иванова", "salary": "5000.00", "date": "2026-09-29", "city": "София", "kind": "Full time",
			"iban": "BG80 BNBG 9661", "agree": "true", "off": "false", "empty": "   ",
		},
		Uploads:   map[string][]byte{"photo": pngBytes(40, 40), "ini2": pngBytes(20, 10), "seal2": pngBytes(30, 30)},
		Signature: pngBytes(120, 40), Name: "Мария Иванова", Date: "2026-09-29 10:00 UTC", Issuer: "Tenant CA",
	}
	res, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Appearance == nil || res.Appearance.Page != 1 || len(res.Appearance.Image) == 0 {
		t.Fatalf("appearance: %+v", res.Appearance)
	}
	if r := res.Appearance.Rect; r[0] >= r[2] || r[1] >= r[3] {
		t.Fatalf("rect %v", r)
	}
	names := map[string]bool{}
	for _, s := range res.Stamps {
		names[s.Name] = true
	}
	for _, want := range []string{"name", "salary", "date", "city", "kind", "iban", "agree", "photo", "sig2", "ini", "ini2", "seal", "seal2"} {
		if !names[want] {
			t.Errorf("missing stamp %s", want)
		}
	}
	for _, not := range []string{"off", "sig", "attach", "empty", "nopic", "nocells"} {
		if names[not] {
			t.Errorf("unexpected stamp %s", not)
		}
	}

	// The stamps and the appearance apply to the document and verify.
	stamped, err := incr.AddStamps(doc, res.Stamps)
	if err != nil {
		t.Fatal(err)
	}
	ca := pdftest.CA("CA")
	id := pdftest.Signer(ca, "Maria")
	signed, err := sign.Sign(stamped, sign.Identity{Signer: id.Key, Chain: id.Chain}, sign.Options{Name: "Maria", Visible: res.Appearance})
	if err != nil {
		t.Fatal(err)
	}
	sigs, err := verify.Verify(signed, verify.Options{})
	if err != nil || len(sigs) != 1 || !sigs[0].Intact {
		t.Fatalf("verify: %v %v", sigs, err)
	}
}

func TestBuildRefusals(t *testing.T) {
	pages := []incr.Page{{X1: 595, Y1: 842}}
	if _, err := Build(Input{Pages: pages, Fields: []store.Field{field("x", "text", 2, 0.1)}}); !errors.Is(err, ErrPage) {
		t.Fatalf("page: %v", err)
	}
	if _, err := Build(Input{Pages: pages, Fields: []store.Field{field("x", "image", 1, 0.1)},
		Uploads: map[string][]byte{"x": []byte("junk")}}); !errors.Is(err, render.ErrImage) {
		t.Fatalf("junk image: %v", err)
	}
	tiny := field("t", "text", 1, 0.1)
	tiny.W, tiny.H = 0.0001, 0.0001
	if _, err := Build(Input{Pages: pages, Fields: []store.Field{tiny}, Values: map[string]string{"t": "x"}}); !errors.Is(err, render.ErrBox) {
		t.Fatalf("tiny: %v", err)
	}
	// A signature on a rotated page gets a rotated appearance; no signature
	// image falls back to the typed name.
	rot := []incr.Page{{X1: 595, Y1: 842, Rotate: 90}}
	res, err := Build(Input{Pages: rot, Fields: []store.Field{field("s", "signature", 1, 0.5)}, Name: "Ivan"})
	if err != nil || res.Appearance == nil {
		t.Fatalf("rotated: %v", err)
	}
	tiny.Type = "signature"
	if _, err := Build(Input{Pages: pages, Fields: []store.Field{tiny}, Name: "x"}); !errors.Is(err, render.ErrBox) {
		t.Fatalf("tiny signature: %v", err)
	}
}

func TestInitials(t *testing.T) {
	if got := Initials("  Мария  Иванова "); got != "М.И." {
		t.Fatalf("got %q", got)
	}
	if Initials("") != "" {
		t.Fatal("empty")
	}
}
