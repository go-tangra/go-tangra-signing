// Package pdftest builds the PDF fixtures and throwaway certificates of the
// signing module's tests at run time (no binary fixtures are committed):
// multi-page contracts with placeholder lines and Cyrillic text, a test CA and
// signer, and a "card" chain for simulated qualified signatures.
package pdftest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/digitorus/timestamp"
	"github.com/signintech/gopdf"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/fonts"
)

// Contract returns an A4 PDF of pages pages. Page 1 carries a Cyrillic title,
// a dotted "Name" placeholder, an underscore "Salary" placeholder and a drawn
// signature line; further pages carry body text.
func Contract(pages int) []byte {
	if pages < 1 {
		pages = 1
	}
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("dejavu", fonts.DejaVuSans); err != nil {
		panic(err)
	}
	for p := 1; p <= pages; p++ {
		pdf.AddPage()
		if err := pdf.SetFont("dejavu", "", 12); err != nil {
			panic(err)
		}
		pdf.SetXY(72, 72)
		if p == 1 {
			must(pdf.Text("Трудов договор / Employment contract"))
			pdf.SetXY(72, 140)
			must(pdf.Text("Name: ......................................"))
			pdf.SetXY(72, 180)
			must(pdf.Text("Salary: ____________________"))
			pdf.SetLineWidth(0.8)
			pdf.Line(72, 700, 272, 700)
			pdf.SetXY(72, 712)
			must(pdf.Text("Signature"))
		} else {
			must(pdf.Text(fmt.Sprintf("Page %d — условия / terms", p)))
		}
	}
	return pdf.GetBytesPdf()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// Identity is a certificate with its key and issuer chain (leaf first).
type Identity struct {
	Cert  *x509.Certificate
	Key   *ecdsa.PrivateKey
	Chain []*x509.Certificate
}

// CA returns a throwaway ECDSA P-256 CA.
func CA(cn string) Identity {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	c, _ := x509.ParseCertificate(der)
	return Identity{Cert: c, Key: key, Chain: []*x509.Certificate{c}}
}

// Signer returns an ECDSA signer certificate issued by ca.
func Signer(ca Identity, cn string) Identity {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		panic(err)
	}
	c, _ := x509.ParseCertificate(der)
	return Identity{Cert: c, Key: key, Chain: []*x509.Certificate{c, ca.Cert}}
}

// RSACard is a simulated qualified "card": an RSA-2048 leaf with
// nonRepudiation issued by its own test CA (leaf first).
type RSACard struct {
	Cert  *x509.Certificate
	Key   *rsa.PrivateKey
	Chain []*x509.Certificate
}

// Card returns a simulated qualified-signature card.
func Card(cn string) RSACard {
	ca := CA("Test Qualified CA")
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn, Country: []string{"BG"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageContentCommitment | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		panic(err)
	}
	c, _ := x509.ParseCertificate(der)
	return RSACard{Cert: c, Key: key, Chain: []*x509.Certificate{c, ca.Cert}}
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// TSA serves RFC 3161 time-stamps from a throwaway authority issued by ca;
// every token carries the time at. Close the server when done.
func TSA(ca Identity, at time.Time) *httptest.Server {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "Test TSA"},
		NotBefore:    time.Now().AddDate(-1, 0, 0),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		panic(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		req, err := timestamp.ParseRequest(body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ts := timestamp.Timestamp{
			HashAlgorithm: req.HashAlgorithm, HashedMessage: req.HashedMessage,
			Time: at, Policy: asn1.ObjectIdentifier{1, 2, 3, 4}, Nonce: req.Nonce,
			AddTSACertificate: true,
		}
		resp, err := ts.CreateResponseWithOpts(cert, key, crypto.SHA256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	}))
}
