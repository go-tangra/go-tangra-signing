package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/templates"
)

// ---- views (never object keys, hashes or tenant ids)

type folderView struct {
	ID        string  `json:"id"`
	ParentID  *string `json:"parent_id"`
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	SortOrder int     `json:"sort_order"`
}

func viewFolder(f store.Folder) folderView {
	return folderView{ID: f.ID, ParentID: f.ParentID, Name: f.Name, Path: f.Path, SortOrder: f.SortOrder}
}

type templateView struct {
	ID                string          `json:"id"`
	FolderID          *string         `json:"folder_id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Tags              []string        `json:"tags"`
	Status            string          `json:"status"`
	FileName          string          `json:"file_name"`
	PDFSize           int64           `json:"pdf_size"`
	PDFPages          int             `json:"pdf_pages"`
	PDFSigned         bool            `json:"pdf_signed"`
	Parties           []store.Party   `json:"parties"`
	Fields            []store.Field   `json:"fields"`
	DefaultExpiryDays *int            `json:"default_expiry_days"`
	DefaultReminder   *store.Reminder `json:"default_reminder"`
	Version           int             `json:"version"`
	CreatedAt         time.Time       `json:"created_at"`
	CreatedBy         string          `json:"created_by"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func viewTemplate(t store.Template) templateView {
	tags, parties, fields := t.Tags, t.Parties, t.Fields
	if tags == nil {
		tags = []string{}
	}
	if parties == nil {
		parties = []store.Party{}
	}
	if fields == nil {
		fields = []store.Field{}
	}
	return templateView{ID: t.ID, FolderID: t.FolderID, Name: t.Name, Description: t.Description, Tags: tags, Status: t.Status,
		FileName: t.FileName, PDFSize: t.PDFSize, PDFPages: t.PDFPages, PDFSigned: t.PDFSigned, Parties: parties, Fields: fields,
		DefaultExpiryDays: t.DefaultExpiryDays, DefaultReminder: t.DefaultReminder, Version: t.Version,
		CreatedAt: t.CreatedAt, CreatedBy: t.CreatedBy, UpdatedAt: t.UpdatedAt}
}

// ---- patch decoding: absent vs null

// fieldsOf decodes a JSON object into raw fields (the OpenAPI validator has
// already refused unknown keys and wrong types).
func fieldsOf(r *http.Request) (map[string]json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	if err := DecodeJSON(r, &raw, 0); err != nil {
		return nil, err
	}
	return raw, nil
}

func isNull(m json.RawMessage) bool { return strings.TrimSpace(string(m)) == "null" }

func optString(raw map[string]json.RawMessage, key string) (*string, error) {
	m, ok := raw[key]
	if !ok {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(m, &s); err != nil {
		return nil, ErrMalformed
	}
	return &s, nil
}

// optNullableID is absent → nil, null → &nil, "id" → &&id.
func optNullableID(raw map[string]json.RawMessage, key string) (**string, error) {
	m, ok := raw[key]
	if !ok {
		return nil, nil
	}
	if isNull(m) {
		var none *string
		return &none, nil
	}
	var s string
	if err := json.Unmarshal(m, &s); err != nil {
		return nil, ErrMalformed
	}
	v := &s
	return &v, nil
}

func optInt(raw map[string]json.RawMessage, key string) (*int, error) {
	m, ok := raw[key]
	if !ok {
		return nil, nil
	}
	var n int
	if err := json.Unmarshal(m, &n); err != nil {
		return nil, ErrMalformed
	}
	return &n, nil
}

func optNullableInt(raw map[string]json.RawMessage, key string) (**int, error) {
	m, ok := raw[key]
	if !ok {
		return nil, nil
	}
	if isNull(m) {
		var none *int
		return &none, nil
	}
	var n int
	if err := json.Unmarshal(m, &n); err != nil {
		return nil, ErrMalformed
	}
	v := &n
	return &v, nil
}

func optNullableReminder(raw map[string]json.RawMessage, key string) (**store.Reminder, error) {
	m, ok := raw[key]
	if !ok {
		return nil, nil
	}
	if isNull(m) {
		var none *store.Reminder
		return &none, nil
	}
	var rm store.Reminder
	if err := json.Unmarshal(m, &rm); err != nil {
		return nil, ErrMalformed
	}
	v := &rm
	return &v, nil
}

// ---- handlers

func (s *Server) registerTemplates(svc *templates.Service, maxPDF int64) {
	s.withSubject("GET", Prefix+"/folders", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		fs, err := svc.ListFolders(r.Context(), subj)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		items := make([]folderView, 0, len(fs))
		for _, f := range fs {
			items = append(items, viewFolder(f))
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	folderInput := func(r *http.Request) (templates.FolderInput, error) {
		raw, err := fieldsOf(r)
		if err != nil {
			return templates.FolderInput{}, err
		}
		var in templates.FolderInput
		if in.Name, err = optString(raw, "name"); err != nil {
			return in, err
		}
		if in.ParentID, err = optNullableID(raw, "parent_id"); err != nil {
			return in, err
		}
		in.SortOrder, err = optInt(raw, "sort_order")
		return in, err
	}
	s.withSubject("POST", Prefix+"/folders", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		in, err := folderInput(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		f, err := svc.CreateFolder(r.Context(), subj, in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewFolder(f))
	})
	s.withSubject("PATCH", Prefix+"/folders/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		in, err := folderInput(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		f, err := svc.UpdateFolder(r.Context(), subj, r.PathValue("id"), in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewFolder(f))
	})
	s.withSubject("DELETE", Prefix+"/folders/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		if err := svc.DeleteFolder(r.Context(), subj, r.PathValue("id")); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.withSubject("GET", Prefix+"/templates", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		q := r.URL.Query()
		f := repo.TemplateFilter{Tag: q.Get("tag"), Status: q.Get("status"), Query: q.Get("q"), Page: queryInt(r, "page"), PageSize: queryInt(r, "page_size")}
		if fid := q.Get("folder_id"); fid != "" {
			if fid == "root" {
				fid = ""
			}
			f.FolderID = &fid
		}
		list, total, err := svc.List(r.Context(), subj, f)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		items := make([]templateView, 0, len(list))
		for _, t := range list {
			items = append(items, viewTemplate(t))
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
	})
	s.withSubject("POST", Prefix+"/templates", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		mp, err := ReadMultipart(r, 8, func(name string) int64 {
			if name == "file" {
				return maxPDF
			}
			return 0
		})
		if err != nil {
			if errors.Is(err, ErrPartTooLarge) {
				err = apperr.PayloadTooLarge
			}
			s.fail(w, r, err)
			return
		}
		file, ok := mp.Files["file"]
		if !ok {
			s.fail(w, r, apperr.InvalidPDF)
			return
		}
		in := templates.CreateInput{Name: mp.Values["name"], Description: mp.Values["description"], FileName: file.FileName, PDF: file.Data}
		if fid := strings.TrimSpace(mp.Values["folder_id"]); fid != "" {
			in.FolderID = &fid
		}
		if tags := mp.Values["tags"]; tags != "" {
			in.Tags = strings.Split(tags, ",")
		}
		t, err := svc.Create(r.Context(), subj, in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewTemplate(t))
	})
	s.withSubject("GET", Prefix+"/templates/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		t, err := svc.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewTemplate(t))
	})
	s.withSubject("PATCH", Prefix+"/templates/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		raw, err := fieldsOf(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in templates.PatchInput
		for _, step := range []func() error{
			func() (err error) { in.Name, err = optString(raw, "name"); return },
			func() (err error) { in.Description, err = optString(raw, "description"); return },
			func() (err error) { in.Status, err = optString(raw, "status"); return },
			func() (err error) { in.FolderID, err = optNullableID(raw, "folder_id"); return },
			func() (err error) { in.DefaultExpiryDays, err = optNullableInt(raw, "default_expiry_days"); return },
			func() (err error) { in.DefaultReminder, err = optNullableReminder(raw, "default_reminder"); return },
			func() error {
				if m, ok := raw["tags"]; ok {
					var tags []string
					if err := json.Unmarshal(m, &tags); err != nil {
						return ErrMalformed
					}
					in.Tags = &tags
				}
				return nil
			},
		} {
			if err := step(); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		t, err := svc.Update(r.Context(), subj, r.PathValue("id"), in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewTemplate(t))
	})
	s.withSubject("PUT", Prefix+"/templates/{id}/fields", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var body struct {
			Version int           `json:"version"`
			Parties []store.Party `json:"parties"`
			Fields  []store.Field `json:"fields"`
		}
		if err := DecodeJSON(r, &body, 4<<20); err != nil {
			s.fail(w, r, err)
			return
		}
		t, err := svc.SaveFields(r.Context(), subj, r.PathValue("id"), body.Version, body.Parties, body.Fields)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, viewTemplate(t))
	})
	s.withSubject("POST", Prefix+"/templates/{id}/clone", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		var body struct {
			Name     string  `json:"name"`
			FolderID *string `json:"folder_id"`
		}
		if err := DecodeJSON(r, &body, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		t, err := svc.Clone(r.Context(), subj, r.PathValue("id"), body.Name, body.FolderID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, viewTemplate(t))
	})
	s.withSubject("DELETE", Prefix+"/templates/{id}", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		if err := svc.Delete(r.Context(), subj, r.PathValue("id")); err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.withSubject("GET", Prefix+"/templates/{id}/pdf", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		rc, t, err := svc.OpenPDF(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer func() { _ = rc.Close() }()
		_ = Download(w, "application/pdf", t.FileName, t.PDFSize, rc)
	})
	s.withSubject("POST", Prefix+"/templates/{id}/detect-fields", func(w http.ResponseWriter, r *http.Request, subj authz.Subjects) {
		fields, err := svc.DetectFields(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"fields": fields})
	})
}
