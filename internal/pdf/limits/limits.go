// Package limits opens untrusted PDFs safely (research D10, SR-006): every
// upload (template, verification, administrator signing) passes through Open
// before anything else touches it. Open bounds the size, sniffs the header,
// validates the structure with pdfcpu (relaxed mode, offline) under a time
// limit with a panic guard, refuses encrypted documents and too many pages,
// and reports whether the document already carries signatures.
//
// pdfcpu's default configuration writes a config directory under the user's
// home and exits the process when it cannot; Config() always returns an
// in-memory, offline configuration instead (the directory is disabled once).
package limits

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// Errors (each maps to a reason code of the API: invalid_pdf, payload_too_large).
var (
	ErrInvalid      = errors.New("limits: not a valid PDF")
	ErrTooLarge     = errors.New("limits: PDF exceeds the size limit")
	ErrEncrypted    = errors.New("limits: encrypted PDFs are not accepted")
	ErrTooManyPages = errors.New("limits: PDF exceeds the page limit")
	ErrTimeout      = errors.New("limits: PDF could not be read in time")
)

// Limits bound one document.
type Limits struct {
	MaxBytes int64
	MaxPages int
	Timeout  time.Duration
}

// Doc describes a document that passed Open.
type Doc struct {
	Pages  int
	Signed bool // already carries at least one signature
}

var disableOnce sync.Once

// Config returns an in-memory, offline pdfcpu configuration in relaxed
// validation mode (never touches the file system or the network).
func Config() *model.Configuration {
	disableOnce.Do(api.DisableConfigDir)
	c := model.NewDefaultConfiguration()
	c.ValidationMode = model.ValidationRelaxed
	c.Offline = true
	c.CheckFileNameExt = false
	return c
}

// sniffWindow is how far into the file the "%PDF-" header may start (the
// specification allows leading garbage; readers accept up to 1 KiB).
const sniffWindow = 1024

// Sniff reports whether b starts like a PDF.
func Sniff(b []byte) bool {
	w := b
	if len(w) > sniffWindow {
		w = w[:sniffWindow]
	}
	return bytes.Contains(w, []byte("%PDF-"))
}

// read is pdfcpu's read + validate; a variable so tests can simulate a hang
// or a panic.
var read = func(data []byte) (*model.Context, error) {
	return api.ReadAndValidate(bytes.NewReader(data), Config())
}

type result struct {
	ctx *model.Context
	err error
}

// Open checks data against l and returns what the module needs to know.
func Open(ctx context.Context, data []byte, l Limits) (*Doc, error) {
	if l.MaxBytes > 0 && int64(len(data)) > l.MaxBytes {
		return nil, ErrTooLarge
	}
	if !Sniff(data) {
		return nil, ErrInvalid
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan result, 1) // buffered: a timed-out reader never blocks
	rd := read                 // captured: a timed-out parse keeps its own reader
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("%w: parser panic", ErrInvalid)}
			}
		}()
		c, err := rd(data)
		ch <- result{ctx: c, err: err}
	}()
	var r result
	select {
	case r = <-ch:
	case <-ctx.Done():
		return nil, ErrTimeout
	}
	if r.err != nil {
		if isEncryptedErr(r.err) {
			return nil, ErrEncrypted
		}
		if errors.Is(r.err, ErrInvalid) {
			return nil, r.err
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, r.err)
	}
	if r.ctx.Encrypt != nil || r.ctx.E != nil {
		return nil, ErrEncrypted
	}
	if r.ctx.PageCount < 1 {
		return nil, fmt.Errorf("%w: no pages", ErrInvalid)
	}
	if l.MaxPages > 0 && r.ctx.PageCount > l.MaxPages {
		return nil, ErrTooManyPages
	}
	return &Doc{Pages: r.ctx.PageCount, Signed: HasSignature(data)}, nil
}

// isEncryptedErr recognises pdfcpu's refusals of password-protected files.
func isEncryptedErr(err error) bool {
	s := err.Error()
	return bytes.Contains([]byte(s), []byte("password")) || bytes.Contains([]byte(s), []byte("encrypt"))
}

// HasSignature reports whether the document carries a signature dictionary
// (a /ByteRange array is present only in signature values).
func HasSignature(data []byte) bool { return bytes.Contains(data, []byte("/ByteRange")) }
