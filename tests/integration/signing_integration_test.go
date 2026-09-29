//go:build integration

package integration

import (
	"io"
	"sync"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
)

// T056: two parallel signers signing at the same moment are serialised by the
// submission row lock: both signatures are present and valid, versions 1 and
// 2 exist, nothing is lost.
func TestConcurrentSignersOnTheDatabase(t *testing.T) {
	e := newEnv(t)
	f := e.setupFlow(t, "parallel", map[string]string{"salary": "5000"})
	e.setupCert(t, "aliceA", "123456")
	e.setupCert(t, "bobA", "654321")
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, s := range []struct {
		tok, slot, pin string
		vals           map[string]string
	}{
		{"aliceA", f.aliceSlot, "123456", map[string]string{"name": "Alice"}},
		{"bobA", f.bobSlot, "654321", map[string]string{"salary": "6000"}},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.sign(s.tok, s.slot, s.pin, s.vals).Code
		}()
	}
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("sign codes %v", codes)
	}
	r := e.call("GET", "/api/signing/v1/submissions/"+f.submission, "adminA", nil, "")
	expect(t, r, 200, "get")
	sub := r.json(t)
	if sub["status"] != "completed" || sub["final_version"].(float64) != 2 {
		t.Fatalf("submission %v", sub)
	}
	for _, v := range []string{"0", "1", "2"} {
		expect(t, e.call("GET", "/api/signing/v1/submissions/"+f.submission+"/document?version="+v, "adminA", nil, ""), 200, "version "+v)
	}
	r = e.call("GET", "/api/signing/v1/submissions/"+f.submission+"/document", "aliceA", nil, "")
	expect(t, r, 200, "final document")
	doc, _ := io.ReadAll(r.Body)
	sigs, err := verify.Verify(doc, verify.Options{})
	if err != nil || len(sigs) != 2 || !sigs[0].Intact || !sigs[1].Intact || !sigs[1].CoversWhole {
		t.Fatalf("final pdf: %+v %v", sigs, err)
	}
}
