//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// T094: list endpoints stay under a second at 10k rows.
func TestListsAtTenThousandRows(t *testing.T) {
	e := newEnv(t)
	db := repodb.New(e.st)
	now := time.Now().UTC()
	for i := 0; i < 10000; i++ {
		if err := db.CreateTemplate(ctx, store.Template{ID: store.NewID(), TenantID: tenantA, Name: fmt.Sprintf("Template %05d", i),
			Status: store.TemplateDraft, PDFKey: "tenants/" + tenantA + "/templates/x.pdf", PDFSHA256: "00", PDFPages: 1,
			Parties: []store.Party{{Key: "p", Name: "P"}}, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{"", "?q=Template%2009", "?status=draft&page=50&page_size=100"} {
		start := time.Now()
		r := e.call("GET", "/api/signing/v1/templates"+q, "adminA", nil, "")
		elapsed := time.Since(start)
		expect(t, r, 200, "list "+q)
		t.Logf("GET /templates%s over 10k rows: %v", q, elapsed)
		if elapsed > time.Second {
			t.Fatalf("GET /templates%s took %v", q, elapsed)
		}
	}
	if _, total, err := db.ListTemplates(ctx, tenantA, repo.TemplateFilter{PageSize: 1}); err != nil || total != 10000 {
		t.Fatalf("total %d %v", total, err)
	}
}
