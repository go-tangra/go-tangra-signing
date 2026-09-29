package httpapi

import (
	"net/http"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/backup"
)

func (s *Server) registerBackup(svc *backup.Service, maxBytes int64) {
	s.withSubject("POST", Prefix+"/backup/export", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		tenant, recs, _, err := svc.Collect(r.Context(), subj, r.URL.Query().Get("tenant_id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/gzip")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Disposition", disposition("signing-"+tenant+"-"+time.Now().UTC().Format("20060102-150405")+".tar.gz"))
		w.WriteHeader(http.StatusOK)
		// Past this point a failure can only cut the stream short (the
		// archive then fails its gzip check on import).
		_ = svc.Write(r.Context(), subj, tenant, recs, w)
	})
	s.withSubject("POST", Prefix+"/backup/import", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		body := http.MaxBytesReader(w, r.Body, maxBytes)
		q := r.URL.Query()
		res, err := svc.Import(r.Context(), subj, q.Get("tenant_id"), q.Get("mode"), body)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
}
