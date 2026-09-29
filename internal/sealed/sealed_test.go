package sealed

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func kek() []byte { return bytes.Repeat([]byte{7}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	e, err := NewEnvelope(kek())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEnvelope([]byte("short")); !errors.Is(err, ErrKEK) {
		t.Fatalf("short kek accepted: %v", err)
	}
	pt := []byte("-----PKCS8 SIGNING-MARKER-KEY-1-----")
	blob, err := e.Seal(pt, ADCertKey("c1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("SIGNING-MARKER-KEY-1")) {
		t.Fatal("plaintext in blob")
	}
	got, err := e.Open(blob, ADCertKey("c1"))
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("open: %v %q", err, got)
	}
	// A key moved to another certificate row, tampering, a wrong KEK and
	// short blobs are refused.
	if _, err := e.Open(blob, ADCertKey("c2")); !errors.Is(err, ErrTampered) {
		t.Fatalf("ad mismatch: %v", err)
	}
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 1
	if _, err := e.Open(bad, ADCertKey("c1")); !errors.Is(err, ErrTampered) {
		t.Fatalf("tamper: %v", err)
	}
	bad = append([]byte(nil), blob...)
	bad[20] ^= 1
	if _, err := e.Open(bad, ADCertKey("c1")); !errors.Is(err, ErrTampered) {
		t.Fatalf("tampered wrap: %v", err)
	}
	e2, _ := NewEnvelope(bytes.Repeat([]byte{8}, 32))
	if _, err := e2.Open(blob, ADCertKey("c1")); !errors.Is(err, ErrTampered) {
		t.Fatalf("wrong kek: %v", err)
	}
	if _, err := e.Open(blob[:10], ADCertKey("c1")); !errors.Is(err, ErrTampered) {
		t.Fatalf("short: %v", err)
	}
	if _, err := e.Seal(bytes.Repeat([]byte("x"), MaxPlaintextBytes+1), ADCertKey("c1")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
}

func TestKeyCheck(t *testing.T) {
	a, _ := NewEnvelope(kek())
	b, _ := NewEnvelope(kek())
	c, _ := NewEnvelope(bytes.Repeat([]byte{8}, 32))
	if len(a.KeyCheck()) != 16 || !bytes.Equal(a.KeyCheck(), b.KeyCheck()) {
		t.Fatal("same KEK must give the same key check")
	}
	if bytes.Equal(a.KeyCheck(), c.KeyCheck()) {
		t.Fatal("different KEKs must give different key checks")
	}
	if bytes.Contains(a.KeyCheck(), kek()[:8]) {
		t.Fatal("key check reveals the KEK")
	}
	kc := a.KeyCheck()
	kc[0] ^= 1
	if bytes.Equal(kc, a.KeyCheck()) {
		t.Fatal("KeyCheck must return a copy")
	}
}

func TestADCertKey(t *testing.T) {
	if got := string(ADCertKey("k1")); got != "certkey:k1" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadKEK(t *testing.T) {
	dir := t.TempDir()
	raw := bytes.Repeat([]byte{9}, 32)
	p := filepath.Join(dir, "kek")
	if err := os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(raw)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := LoadKEK("file", p, "")
	if err != nil || !bytes.Equal(k, raw) {
		t.Fatalf("file base64: %v", err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if k, err := LoadKEK("file", p, ""); err != nil || !bytes.Equal(k, raw) {
		t.Fatalf("file raw: %v", err)
	}
	t.Setenv("SIGNING_KEK", base64.RawStdEncoding.EncodeToString(raw))
	if k, err := LoadKEK("env", "", "SIGNING_KEK"); err != nil || !bytes.Equal(k, raw) {
		t.Fatalf("env: %v", err)
	}
	t.Setenv("SIGNING_KEK", "")
	if _, err := LoadKEK("env", "", "SIGNING_KEK"); err == nil {
		t.Fatal("empty env accepted")
	}
	if _, err := LoadKEK("file", filepath.Join(dir, "missing"), ""); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := LoadKEK("vault", "", ""); err == nil {
		t.Fatal("unknown source accepted")
	}
	if err := os.WriteFile(p, []byte("not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKEK("file", p, ""); !errors.Is(err, ErrKEK) {
		t.Fatalf("bad key: %v", err)
	}
}

// countReader fails after n successful reads.
type countReader struct{ left int }

func (c *countReader) Read(b []byte) (int, error) {
	if c.left == 0 {
		return 0, errors.New("rng down")
	}
	c.left--
	for i := range b {
		b[i] = 1
	}
	return len(b), nil
}

func TestSealRandomFailure(t *testing.T) {
	e, _ := NewEnvelope(kek())
	old := randReader
	defer func() { randReader = old }()
	for n := 0; n < 3; n++ {
		randReader = &countReader{left: n}
		if _, err := e.Seal([]byte("k"), ADCertKey("c")); err == nil {
			t.Fatalf("read %d: expected rng error", n)
		}
	}
	randReader = &countReader{left: 3}
	if _, err := e.Seal([]byte("k"), ADCertKey("c")); err != nil {
		t.Fatal(err)
	}
}

func TestSealData(t *testing.T) {
	e, err := NewEnvelope(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("v"), MaxPlaintextBytes+1)
	if _, err := e.Seal(big, []byte("ad")); err != ErrTooLarge {
		t.Fatalf("Seal bound: %v", err)
	}
	blob, err := e.SealData(big, []byte("ad"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := e.Open(blob, []byte("ad")); err != nil || !bytes.Equal(pt, big) {
		t.Fatalf("open: %v", err)
	}
	if _, err := e.SealData(make([]byte, MaxDataBytes+1), nil); err != ErrTooLarge {
		t.Fatalf("SealData bound: %v", err)
	}
}
