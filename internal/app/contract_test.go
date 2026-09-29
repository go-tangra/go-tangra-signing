package app

import (
	"context"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/httpapi"
)

// Contract (T011): every declared route is implemented, and every route
// that takes a file body declares its own body bound for the gateway.
func TestContract(t *testing.T) {
	a, err := Build(context.Background(), testConfig(), options())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if m := a.HTTP.Missing(); len(m) != 0 {
		t.Fatalf("declared but not implemented: %v", m)
	}
	doc, err := httpapi.LoadDocument()
	if err != nil {
		t.Fatal(err)
	}
	binary := map[string]bool{"createTemplate": true, "sign": true, "prepareQES": true, "signDocument": true,
		"verifyDocument": true, "importBackup": true}
	seen := map[string]bool{}
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			if !binary[op.OperationID] {
				continue
			}
			seen[op.OperationID] = true
			if _, ok := op.Extensions["x-freya-max-body-bytes"]; !ok {
				t.Errorf("%s takes a file body but declares no x-freya-max-body-bytes", op.OperationID)
			}
		}
	}
	for id := range binary {
		if !seen[id] {
			t.Errorf("operation %s not in the document", id)
		}
	}
}
