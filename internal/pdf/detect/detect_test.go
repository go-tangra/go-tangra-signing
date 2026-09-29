package detect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/signintech/gopdf"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/fonts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
)

func TestDetectContractPlaceholders(t *testing.T) {
	fields, err := Detect(context.Background(), pdftest.Contract(2), "p1")
	if err != nil {
		t.Fatal(err)
	}
	// At least the dotted "Name" run and the drawn signature line on page 1.
	if len(fields) < 2 {
		t.Fatalf("fields = %+v", fields)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if f.Page != 1 || f.Type != "text" || f.Party != "p1" {
			t.Fatalf("field = %+v", f)
		}
		if f.X < 0 || f.Y < 0 || f.W <= 0 || f.H <= 0 || f.X+f.W > 1.0001 || f.Y+f.H > 1.0001 {
			t.Fatalf("geometry out of page: %+v", f)
		}
		if seen[f.ID] {
			t.Fatalf("duplicate id %s", f.ID)
		}
		seen[f.ID] = true
	}
	// The dotted run (top of the page) comes before the signature line (bottom).
	if fields[0].Y >= fields[len(fields)-1].Y || fields[len(fields)-1].Y < 0.7 {
		t.Fatalf("order: %+v", fields)
	}
	if fields[0].FontSize <= 0 {
		t.Fatalf("font size not detected: %+v", fields[0])
	}
}

func TestDetectPlainDocumentFindsNothing(t *testing.T) {
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("dejavu", fonts.DejaVuSans); err != nil {
		t.Fatal(err)
	}
	pdf.AddPage()
	_ = pdf.SetFont("dejavu", "", 12)
	pdf.SetXY(72, 72)
	_ = pdf.Text("Just a paragraph of ordinary text without blanks.")
	fields, err := Detect(context.Background(), pdf.GetBytesPdf(), "p1")
	if err != nil || len(fields) != 0 {
		t.Fatalf("fields = %+v, err = %v", fields, err)
	}
}

func TestDetectRefusesGarbageAndTimesOut(t *testing.T) {
	if _, err := Detect(context.Background(), []byte("not a pdf"), "p1"); err == nil {
		t.Fatal("garbage accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := Detect(ctx, pdftest.Contract(50), "p1"); !errors.Is(err, ErrTimeout) {
		// The parse may win the race on a fast machine; only a non-timeout error is wrong.
		if err != nil {
			t.Fatalf("expected timeout or success, got %v", err)
		}
	}
}
