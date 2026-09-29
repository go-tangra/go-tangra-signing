package blob

import (
	"bytes"
	"context"
	"testing"
)

func TestKeys(t *testing.T) {
	const tn = "t1"
	cases := map[string]string{
		TemplatePDF(tn, "tpl", "o"):             "tenants/t1/templates/tpl/o.pdf",
		DocumentVersion(tn, "s", 3):             "tenants/t1/submissions/s/v3.pdf",
		AuditTrail(tn, "s"):                     "tenants/t1/submissions/s/audit-trail.pdf",
		SignerUpload(tn, "s", "sg", "f", "png"): "tenants/t1/submissions/s/signer/sg/f.png",
		QESPrepared(tn, "p"):                    "tenants/t1/qes/p.pdf",
		Upload(tn, "u"):                         "tenants/t1/uploads/u.pdf",
		SubmissionPrefix(tn, "s") + "x":         "tenants/t1/submissions/s/x",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	if !InTenant("tenants/t1/x.pdf", "t1") || InTenant("tenants/t2/x.pdf", "t1") || InTenant("tenants/t1/../t2/x", "t1") ||
		InTenant("tenants/t1/x", "") || InTenant("tenants/t10/x", "t1") {
		t.Fatal("InTenant")
	}
}

func TestFakeListAndHas(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	for _, k := range []string{"tenants/a/2", "tenants/a/1", "tenants/b/1"} {
		if _, err := f.Put(ctx, k, bytes.NewReader([]byte("x")), 1, "application/pdf"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := f.List(ctx, "tenants/a/", 10)
	if len(got) != 2 || got[0] != "tenants/a/1" || got[1] != "tenants/a/2" {
		t.Fatalf("list = %v", got)
	}
	if got, _ := f.List(ctx, "tenants/", 1); len(got) != 1 {
		t.Fatalf("max = %v", got)
	}
	if !f.Has("tenants/b/1") || f.Has("tenants/c/1") {
		t.Fatal("has")
	}
}
