// Package fieldvalues keeps field values out of the database in clear
// (SC-005): a signer's submitted values, a QES preparation's values and a
// submission's prefills are sealed with the module KEK, bound to the row
// they belong to, and opened only where the signing flow needs them. A nil
// envelope stores values as they are (tests of unrelated behaviour).
package fieldvalues

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
)

const (
	mapKey = "_sealed"
	prefix = "sealed:v1:"
)

// ErrSealed is returned for a sealed value that cannot be opened (another
// KEK, a moved row, tampering).
var ErrSealed = errors.New("fieldvalues: sealed value cannot be opened")

// Box seals and opens field values.
type Box struct{ E *sealed.Envelope }

// AD of the sealed records.
func SignerAD(signerID string) string    { return "signer-values:" + signerID }
func PreparationAD(prepID string) string { return "qes-values:" + prepID }
func PrefillAD(submissionID, fieldID string) string {
	return "prefill:" + submissionID + ":" + fieldID
}

// SealMap seals a value map into {"_sealed": "<base64>"}; an empty map stays
// empty.
func (b Box) SealMap(m map[string]string, ad string) (map[string]string, error) {
	if b.E == nil || len(m) == 0 {
		return m, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	blob, err := b.E.SealData(raw, []byte(ad))
	if err != nil {
		return nil, err
	}
	return map[string]string{mapKey: base64.StdEncoding.EncodeToString(blob)}, nil
}

// OpenMap opens a sealed map; a map that is not sealed is returned as is.
func (b Box) OpenMap(m map[string]string, ad string) (map[string]string, error) {
	enc, ok := m[mapKey]
	if !ok || len(m) != 1 {
		return m, nil
	}
	if b.E == nil {
		return nil, ErrSealed
	}
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, ErrSealed
	}
	raw, err := b.E.Open(blob, []byte(ad))
	if err != nil {
		return nil, ErrSealed
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, ErrSealed
	}
	return out, nil
}

// SealString seals one value ("" stays "").
func (b Box) SealString(v, ad string) (string, error) {
	if b.E == nil || v == "" {
		return v, nil
	}
	blob, err := b.E.SealData([]byte(v), []byte(ad))
	if err != nil {
		return "", err
	}
	return prefix + base64.StdEncoding.EncodeToString(blob), nil
}

// OpenString opens a sealed value; an unsealed one is returned as is.
func (b Box) OpenString(v, ad string) (string, error) {
	enc, ok := strings.CutPrefix(v, prefix)
	if !ok {
		return v, nil
	}
	if b.E == nil {
		return "", ErrSealed
	}
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", ErrSealed
	}
	raw, err := b.E.Open(blob, []byte(ad))
	if err != nil {
		return "", ErrSealed
	}
	return string(raw), nil
}
