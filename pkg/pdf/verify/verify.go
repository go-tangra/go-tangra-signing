// Package verify implements PDF digital signature verification.
// Ported from github.com/shurco/goSign with adaptations for the go-tangra ecosystem.
package verify

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pkcs7"
)

// Response contains the verification results for a PDF document.
type Response struct {
	Error        string
	DocumentInfo DocumentInfo
	Signers      []Signer
}

// DocumentInfo holds PDF metadata.
type DocumentInfo struct {
	Author       string
	Creator      string
	Hash         string
	Title        string
	Pages        int
	ModDate      time.Time
	CreationDate time.Time
}

// Signer holds information about a single signature in the PDF.
type Signer struct {
	Name               string
	Reason             string
	Location           string
	ContactInfo        string
	ValidSignature     bool
	TrustedIssuer      bool
	RevokedCertificate bool
	SigFormat          string
	Certificates       []CertificateInfo
}

// CertificateInfo holds information about a certificate in the signature chain.
type CertificateInfo struct {
	Certificate *x509.Certificate
	VerifyError string
}

// Verify verifies all digital signatures in a PDF document.
func Verify(content []byte) (*Response, error) {
	reader := bytes.NewReader(content)
	rdr, err := pdf.NewReader(reader, int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse PDF: %w", err)
	}

	response := &Response{
		DocumentInfo: extractDocumentInfo(rdr),
	}

	// Iterate over all xref objects to find signature dictionaries
	itemCount := rdr.XrefInformation.ItemCount
	for i := uint32(0); i < uint32(itemCount); i++ {
		obj, err := rdr.GetObject(i)
		if err != nil {
			continue
		}
		if obj.Kind() != pdf.Dict && obj.Kind() != pdf.Stream {
			continue
		}

		// Look for signature objects
		filter := obj.Key("Filter").Name()
		if filter != "Adobe.PPKLite" {
			continue
		}

		subFilter := obj.Key("SubFilter").Name()
		signerInfo := Signer{
			SigFormat:   subFilter,
			Name:        obj.Key("Name").Text(),
			Reason:      obj.Key("Reason").Text(),
			Location:    obj.Key("Location").Text(),
			ContactInfo: obj.Key("ContactInfo").Text(),
		}

		// Extract and verify PKCS7 signature
		contentsVal := obj.Key("Contents")
		byteRangeVal := obj.Key("ByteRange")

		contentsBytes := []byte(contentsVal.RawString())
		byteRange := make([]int, byteRangeVal.Len())
		for j := 0; j < byteRangeVal.Len(); j++ {
			byteRange[j] = int(byteRangeVal.Index(j).Int64())
		}

		if len(contentsBytes) > 0 && len(byteRange) == 4 {
			signedContent := extractSignedContent(content, byteRange)

			p7, err := pkcs7.Parse(contentsBytes)
			if err != nil {
				signerInfo.ValidSignature = false
			} else {
				p7.Content = signedContent

				// Try verification with system cert pool
				certPool, _ := x509.SystemCertPool()
				if certPool == nil {
					certPool = x509.NewCertPool()
				}

				if err := p7.VerifyWithChain(certPool); err != nil {
					// Try without trusted chain
					if err := p7.Verify(); err != nil {
						signerInfo.ValidSignature = false
					} else {
						signerInfo.ValidSignature = true
						signerInfo.TrustedIssuer = false
					}
				} else {
					signerInfo.ValidSignature = true
					signerInfo.TrustedIssuer = true
				}

				// Extract certificate info
				for _, cert := range p7.Certificates {
					signerInfo.Certificates = append(signerInfo.Certificates, CertificateInfo{
						Certificate: cert,
					})
				}
			}
		}

		response.Signers = append(response.Signers, signerInfo)
	}

	return response, nil
}

// extractSignedContent reconstructs the signed content from byte ranges.
func extractSignedContent(content []byte, byteRange []int) []byte {
	if len(byteRange) != 4 {
		return nil
	}

	signedContent := make([]byte, 0)
	start1 := byteRange[0]
	len1 := byteRange[1]
	start2 := byteRange[2]
	len2 := byteRange[3]

	if start1+len1 <= len(content) {
		signedContent = append(signedContent, content[start1:start1+len1]...)
	}
	if start2+len2 <= len(content) {
		signedContent = append(signedContent, content[start2:start2+len2]...)
	}

	return signedContent
}

// extractDocumentInfo extracts metadata from the PDF document.
func extractDocumentInfo(rdr *pdf.Reader) DocumentInfo {
	info := DocumentInfo{}

	trailer := rdr.Trailer()
	infoObj := trailer.Key("Info")
	if infoObj.Kind() == pdf.Dict {
		info.Title = infoObj.Key("Title").Text()
		info.Author = infoObj.Key("Author").Text()
		info.Creator = infoObj.Key("Creator").Text()
	}

	// Count pages via the Pages tree
	pages := trailer.Key("Root").Key("Pages")
	if pages.Kind() == pdf.Dict {
		info.Pages = int(pages.Key("Count").Int64())
	}

	return info
}
