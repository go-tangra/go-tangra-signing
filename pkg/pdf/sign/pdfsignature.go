package sign

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
	"golang.org/x/crypto/cryptobyte"
	cryptobyte_asn1 "golang.org/x/crypto/cryptobyte/asn1"
)

const signatureByteRangePlaceholder = "/ByteRange[0 ********** ********** **********]"

func (ctx *SignContext) createSignaturePlaceholder() (dssd string, byteRangeStartByte int64, signatureContentsStartByte int64) {
	var buf bytes.Buffer
	buf.WriteString(strconv.Itoa(int(ctx.SignData.ObjectId)) + " 0 obj\n")
	buf.WriteString("<< /Type /Sig")
	buf.WriteString(" /Filter /Adobe.PPKLite")
	buf.WriteString(" /SubFilter /adbe.pkcs7.detached")

	byteRangeStartByte = int64(buf.Len()) + 1
	buf.WriteString(" " + signatureByteRangePlaceholder)

	signatureContentsStartByte = int64(buf.Len()) + 11
	buf.WriteString(" /Contents<")
	buf.Write(bytes.Repeat([]byte("0"), int(ctx.SignatureMaxLength)))
	buf.WriteString(">")

	switch ctx.SignData.Signature.CertType {
	case CertificationSignature, UsageRightsSignature:
		buf.WriteString(" /Reference [")
		buf.WriteString(" << /Type /SigRef")
	}

	switch ctx.SignData.Signature.CertType {
	case CertificationSignature:
		buf.WriteString(" /TransformMethod /DocMDP")
		buf.WriteString(" /TransformParams <<")
		buf.WriteString(" /Type /TransformParams")
		buf.WriteString(" /P " + strconv.Itoa(int(ctx.SignData.Signature.DocMDPPerm)))
		buf.WriteString(" /V /1.2")
	case UsageRightsSignature:
		buf.WriteString(" /TransformMethod /UR3")
		buf.WriteString(" /TransformParams <<")
		buf.WriteString(" /Type /TransformParams")
		buf.WriteString(" /V /2.2")
	}

	switch ctx.SignData.Signature.CertType {
	case CertificationSignature, UsageRightsSignature:
		buf.WriteString(" >>")
		buf.WriteString(" >>")
		buf.WriteString(" ]")
	}

	if ctx.SignData.Signature.Info.Name != "" {
		buf.WriteString(" /Name " + pdfString(ctx.SignData.Signature.Info.Name))
	}
	if ctx.SignData.Signature.Info.Location != "" {
		buf.WriteString(" /Location " + pdfString(ctx.SignData.Signature.Info.Location))
	}
	if ctx.SignData.Signature.Info.Reason != "" {
		buf.WriteString(" /Reason " + pdfString(ctx.SignData.Signature.Info.Reason))
	}
	if ctx.SignData.Signature.Info.ContactInfo != "" {
		buf.WriteString(" /ContactInfo " + pdfString(ctx.SignData.Signature.Info.ContactInfo))
	}
	buf.WriteString(" /M " + pdfDateTime(ctx.SignData.Signature.Info.Date))
	buf.WriteString(" >>")
	buf.WriteString("\nendobj\n")

	return buf.String(), byteRangeStartByte, signatureContentsStartByte
}

func (ctx *SignContext) fetchRevocationData() error {
	if ctx.SignData.RevocationFunction == nil {
		return nil
	}

	if len(ctx.SignData.CertificateChains) > 0 {
		chain := ctx.SignData.CertificateChains[0]
		for i, cert := range chain {
			var issuer *x509.Certificate
			if i < len(chain)-1 {
				issuer = chain[i+1]
			}
			if err := ctx.SignData.RevocationFunction(cert, issuer, &ctx.SignData.RevocationData); err != nil {
				return err
			}
		}
	}

	// Calculate space needed for revocation data
	for _, crl := range ctx.SignData.RevocationData.CRL {
		ctx.SignatureMaxLength += uint32(hex.EncodedLen(len(crl.FullBytes)))
	}
	for _, ocspResp := range ctx.SignData.RevocationData.OCSP {
		ctx.SignatureMaxLength += uint32(hex.EncodedLen(len(ocspResp.FullBytes)))
	}

	return nil
}

func (ctx *SignContext) createSigningCertificateAttribute() (*pkcs7.Attribute, error) {
	hash := ctx.SignData.DigestAlgorithm.New()
	hash.Write(ctx.SignData.Certificate.Raw)

	var b cryptobyte.Builder
	b.AddASN1(cryptobyte_asn1.SEQUENCE, func(b *cryptobyte.Builder) {
		b.AddASN1(cryptobyte_asn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1(cryptobyte_asn1.SEQUENCE, func(b *cryptobyte.Builder) {
				if ctx.SignData.DigestAlgorithm.HashFunc() != crypto.SHA1 &&
					ctx.SignData.DigestAlgorithm.HashFunc() != crypto.SHA256 {
					b.AddASN1(cryptobyte_asn1.SEQUENCE, func(b *cryptobyte.Builder) {
						b.AddASN1ObjectIdentifier(getOIDFromHashAlgorithm(ctx.SignData.DigestAlgorithm))
					})
				}
				b.AddASN1OctetString(hash.Sum(nil))
			})
		})
	})

	sse, err := b.Bytes()
	if err != nil {
		return nil, err
	}

	attr := pkcs7.Attribute{
		Type:  asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}, // SigningCertificateV2
		Value: asn1.RawValue{FullBytes: sse},
	}
	if ctx.SignData.DigestAlgorithm.HashFunc() == crypto.SHA1 {
		attr.Type = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 12} // SigningCertificate
	}

	return &attr, nil
}

func (ctx *SignContext) createSignature() ([]byte, error) {
	fileContent := ctx.OutputBuffer.Bytes()

	signContent := make([]byte, 0)
	signContent = append(signContent, fileContent[ctx.ByteRangeValues[0]:(ctx.ByteRangeValues[0]+ctx.ByteRangeValues[1])]...)
	signContent = append(signContent, fileContent[ctx.ByteRangeValues[2]:(ctx.ByteRangeValues[2]+ctx.ByteRangeValues[3])]...)

	signedData, err := pkcs7.NewSignedData(signContent)
	if err != nil {
		return nil, fmt.Errorf("new signed data: %w", err)
	}

	signedData.SetDigestAlgorithm(getOIDFromHashAlgorithm(ctx.SignData.DigestAlgorithm))

	signingCert, err := ctx.createSigningCertificateAttribute()
	if err != nil {
		return nil, fmt.Errorf("signing certificate attribute: %w", err)
	}

	signerConfig := pkcs7.SignerInfoConfig{
		ExtraSignedAttributes: []pkcs7.Attribute{
			{
				Type:  asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 8},
				Value: ctx.SignData.RevocationData,
			},
			*signingCert,
		},
	}

	// Certificate chain without our own certificate
	var certChain []*x509.Certificate
	if len(ctx.SignData.CertificateChains) > 0 && len(ctx.SignData.CertificateChains[0]) > 1 {
		certChain = ctx.SignData.CertificateChains[0][1:]
	}

	if err := signedData.AddSignerChain(ctx.SignData.Certificate, ctx.SignData.Signer, certChain, signerConfig); err != nil {
		return nil, fmt.Errorf("add signer chain: %w", err)
	}

	// PDF requires detached signature
	signedData.Detach()

	// Add timestamp if TSA is configured
	if ctx.SignData.TSA.URL != "" {
		sigData := signedData.GetSignedData()
		tsResponse, err := ctx.getTSA(sigData.SignerInfos[0].EncryptedDigest)
		if err != nil {
			return nil, fmt.Errorf("get timestamp: %w", err)
		}

		ts, err := timestamp.ParseResponse(tsResponse)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp: %w", err)
		}

		_, err = pkcs7.Parse(ts.RawToken)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp token: %w", err)
		}

		tsAttr := pkcs7.Attribute{
			Type:  asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14},
			Value: asn1.RawValue{FullBytes: ts.RawToken},
		}
		if err := sigData.SignerInfos[0].SetUnauthenticatedAttributes([]pkcs7.Attribute{tsAttr}); err != nil {
			return nil, err
		}
	}

	return signedData.Finish()
}

func (ctx *SignContext) getTSA(signContent []byte) ([]byte, error) {
	tsRequest, err := timestamp.CreateRequest(bytes.NewReader(signContent), &timestamp.RequestOptions{
		Certificates: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create TSA request: %w", err)
	}

	req, err := http.NewRequest("POST", ctx.SignData.TSA.URL, bytes.NewReader(tsRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Add("Content-Type", "application/timestamp-query")
	req.Header.Add("Content-Transfer-Encoding", "binary")

	if ctx.SignData.TSA.Username != "" && ctx.SignData.TSA.Password != "" {
		req.SetBasicAuth(ctx.SignData.TSA.Username, ctx.SignData.TSA.Password)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TSA request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TSA returned status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

func (ctx *SignContext) replaceSignature() error {
	signature, err := ctx.createSignature()
	if err != nil {
		return fmt.Errorf("failed to create signature: %w", err)
	}

	dst := make([]byte, hex.EncodedLen(len(signature)))
	hex.Encode(dst, signature)

	if uint32(len(dst)) > ctx.SignatureMaxLength {
		// Retry with larger buffer
		ctx.SignatureMaxLengthBase += (uint32(len(dst)) - ctx.SignatureMaxLength) + 1
		return ctx.SignPDF()
	}

	fileContent := ctx.OutputBuffer.Bytes()
	ctx.OutputBuffer.Reset()

	if _, err := ctx.OutputBuffer.Write(fileContent[:(ctx.ByteRangeValues[0] + ctx.ByteRangeValues[1] + 1)]); err != nil {
		return err
	}
	if _, err := ctx.OutputBuffer.Write(dst); err != nil {
		return err
	}
	if _, err := ctx.OutputBuffer.Write(fileContent[(ctx.ByteRangeValues[0]+ctx.ByteRangeValues[1]+1)+int64(len(dst)):]); err != nil {
		return err
	}

	return nil
}
