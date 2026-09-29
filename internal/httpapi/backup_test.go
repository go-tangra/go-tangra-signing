package httpapi

import (
	"bytes"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/backup"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
)

func TestBackupRoutes(t *testing.T) {
	mem, bl := memstore.New(), blob.NewFake()
	s := newAPI(t, Deps{Backup: backup.New(backup.Deps{Store: mem, Blob: bl, KeyCheck: []byte{1, 2, 3}}), MaxBackup: 1 << 20})
	expect(t, call(s, "POST", Prefix+"/backup/export", "member", nil, ""), 403, "members cannot export")
	r := call(s, "POST", Prefix+"/backup/export", "admin", nil, "")
	expect(t, r, 200, "export")
	if r.Header().Get("Content-Type") != "application/gzip" || r.Body.Len() == 0 {
		t.Fatalf("export headers %v", r.Header())
	}
	archive := r.Body.Bytes()
	expect(t, call(s, "POST", Prefix+"/backup/export?tenant_id="+tenantB, "admin", nil, ""), 403, "another tenant without platform admin")
	r = call(s, "POST", Prefix+"/backup/import?mode=overwrite", "admin", bytes.NewReader(archive), "application/gzip")
	expect(t, r, 200, "import")
	if _, ok := r.json(t)["created"]; !ok {
		t.Fatalf("import summary %s", r.Body)
	}
	expect(t, call(s, "POST", Prefix+"/backup/import", "admin", bytes.NewReader([]byte("junk")), "application/gzip"), 422, "garbage")
	expect(t, call(s, "POST", Prefix+"/backup/import?mode=merge", "admin", bytes.NewReader(archive), "application/gzip"), 422, "bad mode (schema)")
}
