package fieldvalues

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
)

func box(t *testing.T, k byte) Box {
	t.Helper()
	e, err := sealed.NewEnvelope(bytes.Repeat([]byte{k}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return Box{E: e}
}

func TestMap(t *testing.T) {
	b := box(t, 1)
	in := map[string]string{"name": "Мария", "salary": "5000"}
	s, err := b.SealMap(in, SignerAD("s1"))
	if err != nil || len(s) != 1 || strings.Contains(s[mapKey], "5000") {
		t.Fatalf("sealed %v %v", s, err)
	}
	out, err := b.OpenMap(s, SignerAD("s1"))
	if err != nil || out["name"] != "Мария" || out["salary"] != "5000" {
		t.Fatalf("open %v %v", out, err)
	}
	if _, err := b.OpenMap(s, SignerAD("s2")); !errors.Is(err, ErrSealed) {
		t.Fatal("moved to another signer")
	}
	if _, err := box(t, 2).OpenMap(s, SignerAD("s1")); !errors.Is(err, ErrSealed) {
		t.Fatal("another KEK")
	}
	if _, err := (Box{}).OpenMap(s, SignerAD("s1")); !errors.Is(err, ErrSealed) {
		t.Fatal("no envelope")
	}
	if _, err := b.OpenMap(map[string]string{mapKey: "!!"}, "x"); !errors.Is(err, ErrSealed) {
		t.Fatal("bad base64")
	}
	notJSON, _ := b.E.SealData([]byte("[1]"), []byte("x"))
	if _, err := b.OpenMap(map[string]string{mapKey: encode(notJSON)}, "x"); !errors.Is(err, ErrSealed) {
		t.Fatal("not a map")
	}
	if m, _ := b.OpenMap(in, "x"); m["name"] != "Мария" {
		t.Fatal("clear maps pass through")
	}
	if m, _ := b.SealMap(nil, "x"); m != nil {
		t.Fatal("empty stays empty")
	}
	if m, _ := (Box{}).SealMap(in, "x"); m["name"] != "Мария" {
		t.Fatal("nil envelope stores as is")
	}
	if _, err := b.SealMap(map[string]string{"x": strings.Repeat("v", sealed.MaxDataBytes)}, "x"); err == nil {
		t.Fatal("too large")
	}
}

func TestString(t *testing.T) {
	b := box(t, 1)
	s, err := b.SealString("5000", PrefillAD("sub", "salary"))
	if err != nil || !strings.HasPrefix(s, prefix) || strings.Contains(s, "5000") {
		t.Fatalf("sealed %q %v", s, err)
	}
	if v, err := b.OpenString(s, PrefillAD("sub", "salary")); err != nil || v != "5000" {
		t.Fatalf("open %q %v", v, err)
	}
	for _, bad := range []struct {
		b  Box
		v  string
		ad string
	}{{b, s, PrefillAD("sub", "other")}, {Box{}, s, "x"}, {b, prefix + "!!", "x"}} {
		if _, err := bad.b.OpenString(bad.v, bad.ad); !errors.Is(err, ErrSealed) {
			t.Errorf("%q opened", bad.v)
		}
	}
	if v, _ := b.OpenString("plain", "x"); v != "plain" {
		t.Fatal("clear passes through")
	}
	if v, _ := b.SealString("", "x"); v != "" {
		t.Fatal("empty stays empty")
	}
	if _, err := b.SealString(strings.Repeat("v", sealed.MaxDataBytes+1), "x"); err == nil {
		t.Fatal("too large")
	}
	if PreparationAD("p") == SignerAD("p") {
		t.Fatal("ADs must differ")
	}
}

func encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
