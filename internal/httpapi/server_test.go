package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-tangra/go-tangra/v4/freyatest/testrt"
	"github.com/go-tangra/go-tangra/v4/freyatest/testutil"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
)

func newServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	s, err := NewHandler(testrt.New(t, testutil.MustCA("example.org"), "signing"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRemoteServingAndFailClosed(t *testing.T) {
	dist := fstest.MapFS{
		"mf-manifest.json":   {Data: []byte(`{"name":"signing"}`)},
		"assets/remote-1.js": {Data: []byte("export {}")},
	}
	s := newServer(t, WithRemote(dist))
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/ui/mf-manifest.json"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("manifest = %d %v", w.Code, w.Header())
	}
	if w := get("/ui/assets/remote-1.js"); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset = %d %v", w.Code, w.Header())
	}
	for _, p := range []string{"/ui/", "/ui/assets/", "/ui/missing.js", "/nothing"} {
		if w := get(p); w.Code != http.StatusNotFound {
			t.Errorf("%s = %d", p, w.Code)
		}
	}
	// No verifier: every protected route refuses; health stays public.
	if w := get(Prefix + "/templates"); w.Code != http.StatusUnauthorized || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("no verifier = %d %v", w.Code, w.Header())
	}
	if w := get(Prefix + "/health"); w.Code != http.StatusNotImplemented {
		t.Fatalf("unwired health = %d", w.Code)
	}
	s.Register(Deps{Health: func() map[string]string { return map[string]string{"db": "ok", "valkey": "down"} }})
	if w := get(Prefix + "/health"); w.Code != 200 || !strings.Contains(w.Body.String(), "degraded") {
		t.Fatalf("health = %d %s", w.Code, w.Body)
	}
	if s.Document() == nil || s.Edge() != nil || s.Permission("GET", Prefix+"/templates") != authz.SigningRead || !s.IsPublic("GET", Prefix+"/health") {
		t.Fatal("accessors")
	}
	if len(s.Declared()) != 51 || len(s.Implemented()) != 1 || len(s.Missing()) != 50 {
		t.Fatalf("declared %d implemented %d missing %d", len(s.Declared()), len(s.Implemented()), len(s.Missing()))
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustHandle of an undeclared route must panic")
		}
	}()
	s.MustHandle("GET", "/nope", func(http.ResponseWriter, *http.Request) {})
}

func TestStatusMapping(t *testing.T) {
	cases := map[error]int{
		ErrConflict:                   409,
		&http.MaxBytesError{Limit: 1}: 413,
		authz.ErrForbidden:            403,
		repo.ErrNotFound:              404,
		repo.ErrConflict:              409,
		errors.New("db down"):         503,
	}
	for err, want := range cases {
		if got, _ := Status(err); got != want {
			t.Errorf("%v => %d, want %d", err, got, want)
		}
	}
	w := httptest.NewRecorder()
	Fail(w, httptest.NewRequest("GET", "/", nil), nil, fmt.Errorf("wrap: %w",
		apperr.PINInvalid.WithDetail(map[string]any{"attempts_left": 2})))
	if w.Code != 403 || w.Body.String() != "{\"detail\":{\"attempts_left\":2},\"reason\":\"pin_invalid\"}\n" {
		t.Fatalf("domain = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	Fail(w, httptest.NewRequest("GET", "/", nil), nil, apperr.MissingRequired.WithField("Salary"))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"Salary"`) {
		t.Fatalf("field = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	Fail(w, httptest.NewRequest("GET", "/", nil), nil, errors.New("secret internals"))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("internal = %d %s", w.Code, w.Body)
	}
	if ErrNotFound.Error() != "not_found" {
		t.Fatal("error text")
	}
}

func TestDecodeJSONAndHeaders(t *testing.T) {
	var v map[string]any
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`))
	if err := DecodeJSON(r, &v, 10); err != ErrBodyTooLarge {
		t.Fatalf("too large: %v", err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"a":1}{}`))
	if err := DecodeJSON(r, &v, 0); err != ErrMalformed {
		t.Fatalf("trailing: %v", err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"a":1}`))
	if err := DecodeJSON(r, &v, 0); err != nil || v["a"] != float64(1) {
		t.Fatalf("ok: %v", err)
	}
	r = httptest.NewRequest("GET", "/?last_id=5-0", nil)
	r.Header.Set("Last-Event-ID", "9-9")
	if lastID(r) != "5-0" {
		t.Fatal("query wins")
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Last-Event-ID", strings.Repeat("9", 65))
	if lastID(r) != "" {
		t.Fatal("oversized header accepted")
	}
	r.Header.Set(HeaderClientAddr, " 10.0.0.7 ")
	r.Header.Set(HeaderUserAgent, strings.Repeat("u", 300))
	if ClientAddr(r) != "10.0.0.7" || len(UserAgent(r)) != 256 {
		t.Fatal("client address / user agent")
	}
	r.Header.Set(HeaderClientAddr, strings.Repeat("1", 80))
	if len(ClientAddr(r)) != 64 {
		t.Fatal("client address bound")
	}
	if queryInt(httptest.NewRequest("GET", "/?n=7", nil), "n") != 7 || queryInt(httptest.NewRequest("GET", "/?n=x", nil), "n") != 0 {
		t.Fatal("queryInt")
	}
	if !queryBool(httptest.NewRequest("GET", "/?b=1", nil), "b") || queryBool(httptest.NewRequest("GET", "/", nil), "b") {
		t.Fatal("queryBool")
	}
}

func TestDownload(t *testing.T) {
	w := httptest.NewRecorder()
	if err := Download(w, "application/pdf", "Трудов договор \"v2\".pdf", 3, strings.NewReader("pdf")); err != nil {
		t.Fatal(err)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="`) || !strings.Contains(cd, "filename*=UTF-8''%D0%A2") || strings.Contains(cd, `\"v2`) {
		t.Fatalf("disposition = %s", cd)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Length") != "3" || w.Body.String() != "pdf" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers = %v", w.Header())
	}
	if d := disposition(""); !strings.Contains(d, "document.pdf") {
		t.Fatalf("default name = %s", d)
	}
}

func multipartBody(t *testing.T, fields map[string]string, files map[string][]byte) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for k, v := range files {
		fw, _ := mw.CreateFormFile(k, k+".bin")
		_, _ = fw.Write(v)
	}
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func TestReadMultipart(t *testing.T) {
	limit := func(name string) int64 {
		if name == "file" {
			return 10
		}
		return 0
	}
	body, ct := multipartBody(t, map[string]string{"name": "Contract"}, map[string][]byte{"file": []byte("0123456789")})
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", ct)
	mp, err := ReadMultipart(r, 5, limit)
	if err != nil || mp.Values["name"] != "Contract" || string(mp.Files["file"].Data) != "0123456789" || mp.Files["file"].FileName != "file.bin" {
		t.Fatalf("parse: %v %+v", err, mp)
	}
	body, ct = multipartBody(t, nil, map[string][]byte{"file": []byte("0123456789X")})
	r = httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", ct)
	if _, err := ReadMultipart(r, 5, limit); !errors.Is(err, ErrPartTooLarge) {
		t.Fatalf("oversized part: %v", err)
	}
	body, ct = multipartBody(t, nil, map[string][]byte{"other": []byte("x")})
	r = httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", ct)
	if _, err := ReadMultipart(r, 5, limit); err != ErrMalformed {
		t.Fatalf("unexpected file part: %v", err)
	}
	body, ct = multipartBody(t, map[string]string{"a": "1", "b": "2", "c": "3"}, nil)
	r = httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", ct)
	if _, err := ReadMultipart(r, 2, limit); err != ErrMalformed {
		t.Fatalf("too many parts: %v", err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader("x"))
	r.Header.Set("Content-Type", "application/json")
	if _, err := ReadMultipart(r, 2, limit); err != ErrMalformed {
		t.Fatalf("not multipart: %v", err)
	}
	// duplicate field name
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("a", "1")
	_ = mw.WriteField("a", "2")
	_ = mw.Close()
	r = httptest.NewRequest("POST", "/", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if _, err := ReadMultipart(r, 5, limit); err != ErrMalformed {
		t.Fatalf("duplicate: %v", err)
	}
	// broken body
	r = httptest.NewRequest("POST", "/", strings.NewReader("--x\r\ngarbage"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if _, err := ReadMultipart(r, 5, limit); err != ErrMalformed {
		t.Fatalf("broken: %v", err)
	}
}
