package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

// Download streams a stored document. The file name goes into
// Content-Disposition (RFC 6266 with an ASCII fallback); the content is never
// sniffed or cached by the browser.
func Download(w http.ResponseWriter, contentType, fileName string, size int64, body io.Reader) error {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", disposition(fileName))
	if size > 0 {
		h.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, body)
	return err
}

// disposition builds an attachment header with an ASCII fallback and a
// UTF-8 filename* for Cyrillic names.
func disposition(name string) string {
	if name == "" {
		name = "document.pdf"
	}
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '/' || r == ';':
			ascii = append(ascii, '_')
		case r > 0x7e:
			ascii = append(ascii, '_')
		default:
			ascii = append(ascii, r)
		}
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": string(ascii)}) +
		"; filename*=UTF-8''" + escapeRFC5987(name)
}

func escapeRFC5987(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Part is one bounded multipart part.
type Part struct {
	Name     string
	FileName string
	Data     []byte
}

// Multipart holds the parts of a bounded multipart/form-data body.
type Multipart struct {
	Values map[string]string
	Files  map[string]Part
}

// ErrPartTooLarge is returned when one part exceeds its limit.
var ErrPartTooLarge = errors.New("httpapi: multipart part too large")

// ReadMultipart parses a multipart/form-data body streaming each part with
// its own bound: file parts up to fileLimit(name) bytes, text fields up to
// 64 KiB, at most maxParts parts. Names are unique (a repeated name is
// refused as malformed).
func ReadMultipart(r *http.Request, maxParts int, fileLimit func(name string) int64) (*Multipart, error) {
	ct := r.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		return nil, ErrMalformed
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	out := &Multipart{Values: map[string]string{}, Files: map[string]Part{}}
	for n := 0; ; n++ {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, ErrBodyTooLarge
			}
			return nil, ErrMalformed
		}
		if n >= maxParts {
			return nil, ErrMalformed
		}
		name := p.FormName()
		if name == "" || len(name) > 128 {
			return nil, ErrMalformed
		}
		if _, dup := out.Values[name]; dup {
			return nil, ErrMalformed
		}
		if _, dup := out.Files[name]; dup {
			return nil, ErrMalformed
		}
		limit := int64(64 << 10)
		isFile := p.FileName() != ""
		if isFile {
			limit = fileLimit(name)
			if limit <= 0 {
				return nil, ErrMalformed
			}
		}
		data, err := io.ReadAll(io.LimitReader(p, limit+1))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, ErrBodyTooLarge
			}
			return nil, ErrMalformed
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("%w: %s", ErrPartTooLarge, name)
		}
		if isFile {
			out.Files[name] = Part{Name: name, FileName: p.FileName(), Data: data}
		} else {
			out.Values[name] = string(data)
		}
	}
}
