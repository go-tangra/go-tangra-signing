package audittrail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/limits"
)

func input(events int) Input {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	signed := now.Add(time.Hour)
	in := Input{SubmissionID: "0190f7c2-0000-7000-8000-000000000001", Name: "Трудов договор\nМария", TemplateName: "Employment",
		Sender: "Sam Sender", CreatedAt: now, CompletedAt: signed, OriginalSHA256: strings.Repeat("a", 64), FinalSHA256: strings.Repeat("b", 64),
		FinalVersion: 2, GeneratedAt: signed,
		Signers: []Signer{
			{Name: "Мария Иванова", Email: "maria@example.org", Party: "Employee", Status: "signed", Method: "local_certificate",
				CertSubject: "Mariya Ivanova", CertSerial: "01AB", CertIssuer: "Tangra Signing CA", IP: "10.0.0.1",
				UserAgent: strings.Repeat("Mozilla/5.0 (X11; Linux x86_64) ", 8), SignedAt: &signed},
			{Name: "Card Holder", Method: "qes", Status: "signed", SignedAt: &signed},
			{Name: "Other", Method: "other", Status: "declined", DeclinedAt: &signed, DeclineReason: "no"},
		}}
	for i := 0; i < events; i++ {
		in.Events = append(in.Events, Event{At: now.Add(time.Duration(i) * time.Minute), Type: fmt.Sprintf("signer.event.%d", i), Actor: "Sam", Signer: "Мария"})
	}
	in.Events = append(in.Events, Event{At: now, Type: "submission.completed"}, Event{At: now, Type: "x", Signer: "S"}, Event{At: now, Type: "y", Actor: "A", Signer: "A"})
	return in
}

func TestBuild(t *testing.T) {
	out, err := Build(input(5))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := limits.Open(context.Background(), out, limits.Limits{})
	if err != nil || doc.Pages != 1 {
		t.Fatalf("pdf: %+v %v", doc, err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatal("not a pdf")
	}
	// Long event lists paginate.
	out, err = Build(input(200))
	if err != nil {
		t.Fatal(err)
	}
	if doc, err := limits.Open(context.Background(), out, limits.Limits{}); err != nil || doc.Pages < 3 {
		t.Fatalf("paginated: %+v %v", doc, err)
	}
	if _, err := Build(Input{Name: "x"}); !errors.Is(err, ErrInput) {
		t.Fatalf("no id: %v", err)
	}
	if stampPtr(nil) != "" || methodName("qes") == "qes" {
		t.Fatal("helpers")
	}
}
