package qes

import (
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
)

// Garbage from the browser never panics and never verifies.
func FuzzNormalizeVerify(f *testing.F) {
	card := pdftest.Card("F")
	f.Add([]byte{0x30, 0x03, 0x02, 0x01, 0x01})
	f.Add(make([]byte, 256))
	f.Fuzz(func(t *testing.T, sig []byte) {
		n, err := Normalize(sig, card.Cert.PublicKey)
		if err == nil && Verify(card.Cert.PublicKey, Digest([]byte("attrs")), n) == nil {
			t.Fatal("random bytes verified")
		}
	})
}

func FuzzParseChain(f *testing.F) {
	f.Add("MIIB", "")
	f.Add("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----", "x")
	f.Fuzz(func(t *testing.T, a, b string) {
		if chain, err := ParseChain([]string{a, b}); err == nil {
			_ = CheckLeaf(chain[0], time.Now())
		}
	})
}
