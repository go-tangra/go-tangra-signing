package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
)

const (
	tenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

// tokens: "admin" (every permission, tenant A), "reader" (signing:read), "member"
// (signing:sign only), "outsider" (every permission, tenant B).
type tokenVerifier map[string]authclient.Identity

func (v tokenVerifier) Verify(_ context.Context, tok string) (authclient.Identity, error) {
	id, ok := v[tok]
	if !ok {
		return authclient.Identity{}, errors.New("unauthenticated")
	}
	return id, nil
}

var testVerifier = tokenVerifier{
	"admin":    {UserID: "admin", TenantID: tenantA},
	"reader":   {UserID: "reader", TenantID: tenantA},
	"member":   {UserID: "member", TenantID: tenantA},
	"outsider": {UserID: "outsider", TenantID: tenantB},
}

var testChecker = authz.Static{
	"admin":    authz.Permissions,
	"reader":   {authz.SigningSign, authz.SigningRead},
	"member":   {authz.SigningSign},
	"outsider": authz.Permissions,
}

func newAPI(t *testing.T, d Deps) *Server {
	t.Helper()
	s := newServer(t, WithVerifier(testVerifier), WithChecker(testChecker))
	s.Register(d)
	return s
}

type resp struct {
	*httptest.ResponseRecorder
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatalf("not json (%d): %s", r.Code, r.Body)
	}
	return m
}

func (r resp) reason() string {
	var m map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &m)
	s, _ := m["reason"].(string)
	return s
}

func call(s *Server, method, path, token string, body io.Reader, contentType string) resp {
	r := httptest.NewRequest(method, "https://localhost"+path, body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if method != "GET" {
		r.Header.Set("X-CSRF-Token", "t")
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return resp{w}
}

func callJSON(s *Server, method, path, token string, v any) resp {
	if v == nil {
		return call(s, method, path, token, nil, "")
	}
	b, _ := json.Marshal(v)
	return call(s, method, path, token, bytes.NewReader(b), "application/json")
}

func multipartReq(fields map[string]string, files map[string][]byte) (*bytes.Buffer, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for k, v := range files {
		fw, _ := mw.CreateFormFile(k, "Договор.pdf")
		_, _ = fw.Write(v)
	}
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func expect(t *testing.T, r resp, status int, what string) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("%s: status %d, want %d: %s", what, r.Code, status, r.Body)
	}
}

var _ = http.StatusOK
