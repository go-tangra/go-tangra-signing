package signing

import (
	"bytes"
	"crypto/rand"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/signintech/gopdf"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/fonts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// bigPDF builds a pages-page A4 document of roughly size bytes (noise JPEGs).
func bigPDF(t testing.TB, pages int, size int) []byte {
	t.Helper()
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("dejavu", fonts.DejaVuSans); err != nil {
		t.Fatal(err)
	}
	per := size / pages
	for p := 0; p < pages; p++ {
		pdf.AddPage()
		_ = pdf.SetFont("dejavu", "", 12)
		pdf.SetXY(72, 72)
		_ = pdf.Text("Страница / page")
		// A noise image does not compress: its size drives the file size.
		side := 200
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		noise := make([]byte, side*side*4)
		_, _ = rand.Read(noise)
		copy(img.Pix, noise)
		var buf bytes.Buffer
		q := 95
		for {
			buf.Reset()
			_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
			if buf.Len() >= per || side >= 1400 {
				break
			}
			side += 100
			img = image.NewRGBA(image.Rect(0, 0, side, side))
			noise = make([]byte, side*side*4)
			_, _ = rand.Read(noise)
			copy(img.Pix, noise)
			for i := 3; i < len(img.Pix); i += 4 {
				img.Pix[i] = 255
			}
		}
		h, err := gopdf.ImageHolderByBytes(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if err := pdf.ImageByHolder(h, 72, 120, &gopdf.Rect{W: 450, H: 450}); err != nil {
			t.Fatal(err)
		}
	}
	return pdf.GetBytesPdf()
}

// SC-003: a 50-page 20 MB document signs locally in under 5 s.
func TestLargeDocumentSignsQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("performance check")
	}
	doc := bigPDF(t, 50, 20<<20)
	if len(doc) < 15<<20 {
		t.Fatalf("fixture only %d bytes", len(doc))
	}
	ca := pdftest.CA("CA")
	id := pdftest.Signer(ca, "Signer")
	fields := []store.Field{
		{ID: "n", Name: "N", Type: "text", Page: 1, X: 0.1, Y: 0.1, W: 0.3, H: 0.03},
		{ID: "s", Name: "S", Type: "signature", Page: 50, X: 0.6, Y: 0.8, W: 0.3, H: 0.08},
	}
	start := time.Now()
	stamped, appearance, err := Stamped(doc, fields, map[string]string{"n": "Мария Иванова"}, nil, nil, "Signer", "CA", start)
	if err != nil {
		t.Fatal(err)
	}
	out, err := sign.Sign(stamped, sign.Identity{Signer: id.Key, Chain: id.Chain}, sign.Options{Name: "Signer", Visible: appearance})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("%d pages, %.1f MB: stamped and signed in %v", 50, float64(len(doc))/(1<<20), elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("took %v (SC-003: under 5 s)", elapsed)
	}
	if !bytes.HasPrefix(out, doc) {
		t.Fatal("not incremental")
	}
	if _, err := incr.Pages(out); err != nil {
		t.Fatal(err)
	}
}
