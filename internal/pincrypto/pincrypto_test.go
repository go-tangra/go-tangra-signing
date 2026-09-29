package pincrypto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

// fast keeps the tests quick; production uses 600k (config).
const fast = MinIterations

func TestSealOpenRoundTrip(t *testing.T) {
	key := []byte("PKCS8-SIGNER-KEY-MARKER")
	blob, err := Seal(key, "123456", []byte("cert:1"), fast)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, key) || bytes.Contains(blob, []byte("123456")) {
		t.Fatal("key or PIN in the envelope")
	}
	got, err := Open(blob, "123456", []byte("cert:1"))
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("open: %v", err)
	}
	// Wrong PIN, moved to another certificate, tampered ciphertext: ErrPIN.
	if _, err := Open(blob, "654321", []byte("cert:1")); !errors.Is(err, ErrPIN) {
		t.Fatalf("wrong pin: %v", err)
	}
	if _, err := Open(blob, "123456", []byte("cert:2")); !errors.Is(err, ErrPIN) {
		t.Fatalf("moved blob: %v", err)
	}
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 1
	if _, err := Open(bad, "123456", []byte("cert:1")); !errors.Is(err, ErrPIN) {
		t.Fatalf("tampered: %v", err)
	}
	// Two seals of the same key differ (fresh salt and nonce).
	blob2, _ := Seal(key, "123456", []byte("cert:1"), fast)
	if bytes.Equal(blob, blob2) {
		t.Fatal("deterministic envelope")
	}
}

func TestFormatRefusals(t *testing.T) {
	blob, _ := Seal([]byte("k"), "123456", nil, fast)
	cases := map[string][]byte{
		"short":   blob[:10],
		"version": append([]byte{9}, blob[1:]...),
	}
	low := append([]byte(nil), blob...)
	binary.BigEndian.PutUint32(low[1:5], 10)
	cases["weak iterations"] = low
	huge := append([]byte(nil), blob...)
	binary.BigEndian.PutUint32(huge[1:5], MaxIterations+1)
	cases["huge iterations"] = huge
	for name, b := range cases {
		if _, err := Open(b, "123456", nil); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Seal([]byte("k"), "123456", nil, 1000); !errors.Is(err, ErrFormat) {
		t.Fatal("weak iterations sealed")
	}
	if _, err := Seal([]byte("k"), "123456", nil, MaxIterations+1); !errors.Is(err, ErrFormat) {
		t.Fatal("huge iterations sealed")
	}
}

type failAfter struct{ n int }

func (f *failAfter) Read(b []byte) (int, error) {
	if f.n == 0 {
		return 0, errors.New("rng down")
	}
	f.n--
	return len(b), nil
}

func TestSealRandomFailure(t *testing.T) {
	old := randReader
	defer func() { randReader = old }()
	for n := 0; n < 2; n++ {
		randReader = &failAfter{n: n}
		if _, err := Seal([]byte("k"), "123456", nil, fast); err == nil {
			t.Fatalf("rng failure %d hidden", n)
		}
	}
}

func TestDeriveFailure(t *testing.T) {
	blob, _ := Seal([]byte("k"), "123456", nil, fast)
	old := derive
	defer func() { derive = old }()
	derive = func(string, []byte, int) ([]byte, error) { return nil, errors.New("fips refusal") }
	if _, err := Seal([]byte("k"), "123456", nil, fast); err == nil {
		t.Fatal("seal hid a derivation failure")
	}
	if _, err := Open(blob, "123456", nil); err == nil || errors.Is(err, ErrPIN) {
		t.Fatalf("open: %v", err)
	}
}

func TestRules(t *testing.T) {
	r := Rules{Min: 6, Max: 32}
	for _, ok := range []string{"123456", "пинкод", strings.Repeat("a", 32), "a b c d"} {
		if err := r.Check(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"12345", strings.Repeat("a", 33), "12345\n", "\xff\xfe12345"} {
		if err := r.Check(bad); !errors.Is(err, ErrRule) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLockout(t *testing.T) {
	now := time.Now()
	l := Lockout{Attempts: 5, Duration: 15 * time.Minute}
	until := l.Until(now)
	if !Locked(&until, now) || Locked(&until, until) || Locked(nil, now) {
		t.Fatal("locked")
	}
	if l.AttemptsLeft(2) != 3 || l.AttemptsLeft(7) != 0 {
		t.Fatal("attempts left")
	}
}

func FuzzOpen(f *testing.F) {
	blob, _ := Seal([]byte("key"), "123456", []byte("ad"), fast)
	f.Add(blob, "123456")
	f.Add([]byte{1, 0, 1, 134, 160}, "x")
	f.Fuzz(func(t *testing.T, b []byte, pin string) {
		if len(b) > 5 {
			// Keep the derivation cheap: clamp the iteration count to the minimum.
			binary.BigEndian.PutUint32(b[1:5], MinIterations)
		}
		pt, err := Open(b, pin, []byte("ad"))
		if err == nil && pt == nil {
			t.Fatal("nil plaintext without error")
		}
	})
}
