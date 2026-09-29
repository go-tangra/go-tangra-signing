package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/signing"
	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
	"github.com/go-tangra/go-tangra-signing/v4/internal/templates"
)

// Prefix of the browser API.
const Prefix = "/api/signing/v1"

// Deps wire the HTTP handlers. A route whose service is not wired answers 501
// not_implemented.
type Deps struct {
	Hub         *stream.Hub
	Health      func() map[string]string // component status for /health
	Templates   *templates.Service       // folders, templates, builder, detection
	MaxPDFBytes int64                    // upload bound (limits_signing.max_pdf_bytes)
	Me          *certs.Me                // the caller's own certificate
	Submissions *submissions.Service     // submissions, inbox, member picker
	Signing     *signing.Service         // signing sessions
	MaxImage    int64                    // signature/image upload bound (limits_signing.max_image_bytes)
	MaxUpload   int64                    // field upload bound (limits_signing.max_field_upload_bytes)
}

// Register mounts the handlers of every wired dependency.
func (s *Server) Register(d Deps) {
	s.MustHandle("GET", Prefix+"/health", func(w http.ResponseWriter, _ *http.Request) {
		out := map[string]any{"status": "ok"}
		if d.Health != nil {
			comps := d.Health()
			for _, v := range comps {
				if v != "ok" {
					out["status"] = "degraded"
				}
			}
			out["components"] = comps
		}
		WriteJSON(w, http.StatusOK, out)
	})
	if d.Hub != nil {
		s.RegisterStream(d.Hub)
	}
	if d.Templates != nil {
		s.registerTemplates(d.Templates, d.MaxPDFBytes)
	}
	if d.Me != nil {
		s.registerMe(d.Me)
	}
	if d.Submissions != nil {
		s.registerSubmissions(d.Submissions)
	}
	if d.Signing != nil {
		maxImage := d.MaxImage
		if maxImage <= 0 {
			maxImage = 1 << 20
		}
		maxUpload := max(d.MaxUpload, maxImage)
		s.registerSigning(d.Signing, maxUpload, maxImage)
	}
}

// withSubject wraps a handler needing the verified caller.
func (s *Server) withSubject(method, path string, fn func(w http.ResponseWriter, r *http.Request, subj authz.Subjects)) {
	s.MustHandle(method, path, func(w http.ResponseWriter, r *http.Request) {
		subj, err := Subjects(r)
		if err != nil {
			Fail(w, r, nil, err)
			return
		}
		fn(w, r, subj)
	})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) { Fail(w, r, s.log, err) }

func queryInt(r *http.Request, name string) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return 0
	}
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "true" || v == "1"
}
