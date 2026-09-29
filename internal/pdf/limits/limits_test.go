package limits

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
)

var bg = context.Background()

func TestOpenContract(t *testing.T) {
	doc, err := Open(bg, pdftest.Contract(3), Limits{MaxBytes: 5 << 20, MaxPages: 10, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Pages != 3 || doc.Signed {
		t.Fatalf("doc = %+v", doc)
	}
	// Defaults apply when no timeout is given.
	if _, err := Open(bg, pdftest.Contract(1), Limits{}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRefusals(t *testing.T) {
	c := pdftest.Contract(3)
	if _, err := Open(bg, c, Limits{MaxBytes: 100}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
	if _, err := Open(bg, c, Limits{MaxPages: 2}); !errors.Is(err, ErrTooManyPages) {
		t.Fatalf("pages: %v", err)
	}
	for name, data := range map[string][]byte{
		"text":      []byte("hello, this is not a pdf at all"),
		"empty":     nil,
		"truncated": c[:len(c)/3],
		"header":    []byte("%PDF-1.7\n%garbage\n"),
		"late hdr":  append(bytes.Repeat([]byte{' '}, 2000), c...),
	} {
		if _, err := Open(bg, data, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOpenEncrypted(t *testing.T) {
	var out bytes.Buffer
	conf := Config()
	conf.UserPW, conf.OwnerPW = "user-secret", "owner-secret"
	conf.EncryptUsingAES, conf.EncryptKeyLength = true, 256
	conf.Cmd = model.ENCRYPT
	if err := api.Encrypt(bytes.NewReader(pdftest.Contract(1)), &out, conf); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bg, out.Bytes(), Limits{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("user password: %v", err)
	}
	// Owner password only (opens without a password): still refused.
	out.Reset()
	conf = Config()
	conf.OwnerPW = "owner-secret"
	conf.EncryptUsingAES, conf.EncryptKeyLength = true, 128
	conf.Cmd = model.ENCRYPT
	if err := api.Encrypt(bytes.NewReader(pdftest.Contract(1)), &out, conf); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bg, out.Bytes(), Limits{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("owner password: %v", err)
	}
}

func TestOpenTimeoutAndPanic(t *testing.T) {
	old := read
	defer func() { read = old }()
	block := make(chan struct{})
	defer close(block)
	read = func([]byte) (*model.Context, error) { <-block; return nil, nil }
	if _, err := Open(bg, pdftest.Contract(1), Limits{Timeout: 20 * time.Millisecond}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	read = func([]byte) (*model.Context, error) { panic("boom") }
	if _, err := Open(bg, pdftest.Contract(1), Limits{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("panic: %v", err)
	}
	read = func([]byte) (*model.Context, error) { return &model.Context{XRefTable: &model.XRefTable{}}, nil }
	if _, err := Open(bg, pdftest.Contract(1), Limits{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no pages: %v", err)
	}
	read = func([]byte) (*model.Context, error) { return nil, errors.New("wrong password supplied") }
	if _, err := Open(bg, pdftest.Contract(1), Limits{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("password error: %v", err)
	}
}

func TestSniffAndSignature(t *testing.T) {
	if !Sniff([]byte("%PDF-1.4")) || Sniff([]byte("PK\x03\x04")) || Sniff(append(bytes.Repeat([]byte{0}, 1100), "%PDF-"...)) {
		t.Fatal("sniff")
	}
	if !HasSignature([]byte("<< /Type /Sig /ByteRange [0 1 2 3] >>")) || HasSignature(pdftest.Contract(1)) {
		t.Fatal("signature detection")
	}
}

func FuzzOpen(f *testing.F) {
	f.Add(pdftest.Contract(1))
	f.Add([]byte("%PDF-1.7\n1 0 obj<<>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF"))
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Open(bg, data, Limits{MaxBytes: 1 << 20, MaxPages: 20, Timeout: 2 * time.Second})
		if err == nil && (doc.Pages < 1 || doc.Pages > 20) {
			t.Fatalf("accepted doc with %d pages", doc.Pages)
		}
	})
}
