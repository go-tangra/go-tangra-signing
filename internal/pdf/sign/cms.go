package sign

import (
	"bytes"
	"errors"
)

// A minimal DER walker for the one edit the external (QES) flow needs:
// replacing SignerInfo.encryptedDigest of a detached CMS SignedData while
// every other byte keeps its encoding (lengths are recomputed).

var errDER = errors.New("sign: malformed CMS")

type tlv struct {
	tag      []byte // identifier octets
	children []*tlv // constructed
	value    []byte // primitive contents
	cons     bool
}

const maxDepth = 32

func parseTLV(b []byte, depth int) (*tlv, []byte, error) {
	if depth > maxDepth || len(b) < 2 {
		return nil, nil, errDER
	}
	i := 1
	if b[0]&0x1f == 0x1f { // high tag number
		for i < len(b) && b[i]&0x80 != 0 {
			i++
		}
		i++
	}
	if i >= len(b) {
		return nil, nil, errDER
	}
	tag := b[:i]
	l := int(b[i])
	i++
	if l&0x80 != 0 {
		n := l & 0x7f
		if n == 0 || n > 4 || i+n > len(b) {
			return nil, nil, errDER
		}
		l = 0
		for _, c := range b[i : i+n] {
			l = l<<8 | int(c)
		}
		i += n
	}
	if l < 0 || i+l > len(b) {
		return nil, nil, errDER
	}
	content, rest := b[i:i+l], b[i+l:]
	t := &tlv{tag: tag, cons: tag[0]&0x20 != 0}
	if !t.cons {
		t.value = content
		return t, rest, nil
	}
	for len(content) > 0 {
		c, r, err := parseTLV(content, depth+1)
		if err != nil {
			return nil, nil, err
		}
		t.children = append(t.children, c)
		content = r
	}
	return t, rest, nil
}

func lengthBytes(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var out []byte
	for x := n; x > 0; x >>= 8 {
		out = append([]byte{byte(x)}, out...)
	}
	return append([]byte{0x80 | byte(len(out))}, out...)
}

func (t *tlv) encode() []byte {
	var body []byte
	if t.cons {
		var buf bytes.Buffer
		for _, c := range t.children {
			buf.Write(c.encode())
		}
		body = buf.Bytes()
	} else {
		body = t.value
	}
	out := append([]byte{}, t.tag...)
	out = append(out, lengthBytes(len(body))...)
	return append(out, body...)
}

// signerInfo navigates ContentInfo → [0] → SignedData → signerInfos SET → first SignerInfo.
func signerInfo(root *tlv) (*tlv, error) {
	if !root.cons || len(root.children) < 2 {
		return nil, errDER
	}
	explicit := root.children[1]
	if !explicit.cons || len(explicit.children) != 1 {
		return nil, errDER
	}
	sd := explicit.children[0]
	if !sd.cons || len(sd.children) < 4 {
		return nil, errDER
	}
	infos := sd.children[len(sd.children)-1]
	if !infos.cons || infos.tag[0] != 0x31 || len(infos.children) == 0 {
		return nil, errDER
	}
	si := infos.children[0]
	if !si.cons || len(si.children) < 5 {
		return nil, errDER
	}
	return si, nil
}

// encryptedDigest is the OCTET STRING after signatureAlgorithm (index 4, or
// 5 when signedAttrs [0] is present).
func encryptedDigest(si *tlv) (*tlv, error) {
	idx := 4
	if len(si.children) > 3 && si.children[3].tag[0] == 0xa0 {
		idx = 5
	}
	if idx >= len(si.children) || si.children[idx].tag[0] != 0x04 {
		return nil, errDER
	}
	return si.children[idx], nil
}

// signedAttrsDER returns the signed attributes re-tagged as SET (what is
// digested and signed).
func signedAttrsDER(si *tlv) ([]byte, error) {
	if len(si.children) < 4 || si.children[3].tag[0] != 0xa0 {
		return nil, errDER
	}
	enc := si.children[3].encode()
	enc[0] = 0x31
	return enc, nil
}

// replaceSignature returns cms with the first SignerInfo's signature replaced.
func replaceSignature(cms, signature []byte) ([]byte, error) {
	root, rest, err := parseTLV(cms, 0)
	if err != nil || len(rest) != 0 {
		return nil, errDER
	}
	si, err := signerInfo(root)
	if err != nil {
		return nil, err
	}
	ed, err := encryptedDigest(si)
	if err != nil {
		return nil, err
	}
	ed.value = signature
	return root.encode(), nil
}

// inspect returns the signed attributes and the signature of a CMS.
func inspect(cms []byte) (attrs, sig []byte, err error) {
	root, rest, err := parseTLV(cms, 0)
	if err != nil || len(rest) != 0 {
		return nil, nil, errDER
	}
	si, err := signerInfo(root)
	if err != nil {
		return nil, nil, err
	}
	if attrs, err = signedAttrsDER(si); err != nil {
		return nil, nil, err
	}
	ed, err := encryptedDigest(si)
	if err != nil {
		return nil, nil, err
	}
	return attrs, ed.value, nil
}
