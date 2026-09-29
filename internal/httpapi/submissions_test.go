package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/signing"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

type okSender struct{}

func (okSender) SendKey(context.Context, string, string, string, map[string]string, string) (notifyclient.Result, error) {
	return notifyclient.Result{Sent: true}, nil
}

type subsEnv struct {
	s    *Server
	blob *blob.Fake
	mem  *memstore.Mem
	me   *certs.Me
	tpl  store.Template
}

func subsAPI(t *testing.T) subsEnv {
	t.Helper()
	mem, bl := memstore.New(), blob.NewFake()
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	p := pki.New(pki.Deps{Store: mem, Sealer: sealer,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenantA: {
		{UserID: "admin", DisplayName: "Ada Admin", Email: "ada@example.org"},
		{UserID: "member", DisplayName: "Ivan Petrov", Email: "ivan@example.org"},
		{UserID: "reader", DisplayName: "Rita Reader", Email: "rita@example.org"},
	}}}
	me := certs.New(certs.Deps{Store: mem, PKI: p, Contacts: dir, Rules: pincrypto.Rules{Min: 6, Max: 32},
		Lockout: pincrypto.Lockout{Attempts: 5, Duration: 15 * time.Minute}})
	subs := submissions.New(submissions.Deps{Store: mem, Blob: bl, Checker: testChecker, Contacts: dir,
		Mail: mail.Mailer{Sender: okSender{}, PortalBaseURL: "https://portal.example.org"}})
	sg := signing.New(signing.Deps{Store: mem, Blob: bl, Subs: subs, Me: me, PKI: p})

	pdf := pdftest.Contract(1)
	id := store.NewID()
	key := blob.TemplatePDF(tenantA, id, store.NewID())
	sum, _ := bl.Put(context.Background(), key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	now := time.Now().UTC()
	tpl := store.Template{ID: id, TenantID: tenantA, Name: "Договор", Status: store.TemplateActive, PDFKey: key, PDFSHA256: sum,
		PDFSize: int64(len(pdf)), PDFPages: 1, FileName: "c.pdf",
		Parties: []store.Party{{Key: "employee", Name: "Employee"}, {Key: "employer", Name: "Employer"}},
		Fields: []store.Field{
			{ID: "name", Name: "Name", Type: "text", Party: "employee", Page: 1, X: 0.2, Y: 0.15, W: 0.4, H: 0.03, Required: true},
			{ID: "photo", Name: "Photo", Type: "image", Party: "employee", Page: 1, X: 0.6, Y: 0.3, W: 0.2, H: 0.1},
			{ID: "esig", Name: "Signature", Type: "signature", Party: "employee", Page: 1, X: 0.1, Y: 0.8, W: 0.3, H: 0.08},
			{ID: "rsig", Name: "Employer", Type: "signature", Party: "employer", Page: 1, X: 0.6, Y: 0.8, W: 0.3, H: 0.08},
		},
		Version: 1, CreatedAt: now, CreatedBy: "admin", UpdatedAt: now, UpdatedBy: "admin"}
	if err := mem.CreateTemplate(context.Background(), tpl); err != nil {
		t.Fatal(err)
	}
	return subsEnv{blob: bl, s: newAPI(t, Deps{Submissions: subs, Signing: sg, MaxPDFBytes: 5 << 20, MaxUpload: 5 << 20}), mem: mem, me: me, tpl: tpl}
}

func pngData() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 20, 10)))
	return b.Bytes()
}

func (e subsEnv) create(t *testing.T) (string, string, string) {
	t.Helper()
	r := callJSON(e.s, "POST", Prefix+"/submissions", "admin", map[string]any{
		"template_id": e.tpl.ID, "mode": "sequential",
		"signers": []map[string]any{{"user_id": "member", "party": "employee"}, {"user_id": "admin", "party": "employer", "position": 1}},
	})
	expect(t, r, 201, "create")
	body := r.json(t)
	id := body["id"].(string)
	var member, admin string
	for _, s := range body["signers"].([]any) {
		m := s.(map[string]any)
		if m["user_id"] == "member" {
			member = m["id"].(string)
		} else {
			admin = m["id"].(string)
		}
		if _, leaked := m["email"]; leaked {
			t.Fatal("e-mail address in the view")
		}
	}
	return id, member, admin
}

func TestSubmissionRoutes(t *testing.T) {
	e := subsAPI(t)
	s := e.s
	id, memberSlot, adminSlot := e.create(t)
	expect(t, callJSON(s, "POST", Prefix+"/submissions", "member", map[string]any{"template_id": e.tpl.ID, "mode": "parallel",
		"signers": []map[string]any{{"user_id": "member", "party": "employee"}}}), 403, "member cannot create")
	if r := callJSON(s, "POST", Prefix+"/submissions", "admin", map[string]any{"template_id": e.tpl.ID, "mode": "parallel",
		"signers": []map[string]any{{"user_id": "member", "party": "employee"}}}); r.Code != 422 || r.reason() != "invalid_signer" {
		t.Fatalf("one signer for two parties: %d %s", r.Code, r.Body)
	}
	r := call(s, "GET", Prefix+"/submissions?status=draft", "admin", nil, "")
	expect(t, r, 200, "list")
	if r.json(t)["total"].(float64) != 1 {
		t.Fatalf("list %s", r.Body)
	}
	expect(t, call(s, "GET", Prefix+"/submissions/"+id, "outsider", nil, ""), 404, "other tenant")
	expect(t, call(s, "POST", Prefix+"/submissions/"+id+"/send", "member", nil, ""), 403, "signer cannot send")
	r = call(s, "POST", Prefix+"/submissions/"+id+"/send", "admin", nil, "")
	expect(t, r, 200, "send")
	if r.json(t)["status"] != "in_progress" {
		t.Fatalf("send %s", r.Body)
	}

	r = call(s, "GET", Prefix+"/inbox", "member", nil, "")
	expect(t, r, 200, "inbox")
	items := r.json(t)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["signer_id"] != memberSlot {
		t.Fatalf("inbox %s", r.Body)
	}
	r = call(s, "GET", Prefix+"/signing/"+memberSlot, "member", nil, "")
	expect(t, r, 200, "session")
	sess := r.json(t)
	if sess["can_sign"] != true || len(sess["fields"].([]any)) != 3 || sess["certificate"].(map[string]any)["state"] != "none" {
		t.Fatalf("session %s", r.Body)
	}
	expect(t, call(s, "GET", Prefix+"/signing/"+memberSlot, "admin", nil, ""), 404, "someone else's session")
	expect(t, call(s, "POST", Prefix+"/signing/"+memberSlot+"/open", "member", nil, ""), 204, "open")
	r = call(s, "GET", Prefix+"/signing/"+memberSlot+"/document", "member", nil, "")
	expect(t, r, 200, "session document")
	if !bytes.HasPrefix(r.Body.Bytes(), []byte("%PDF")) || !strings.Contains(r.Header().Get("Content-Disposition"), "filename*=UTF-8''") {
		t.Fatal("session document headers")
	}

	sign := func(slot, token string, fields map[string]string, files map[string][]byte) resp {
		body, ct := multipartReq(fields, files)
		return call(s, "POST", Prefix+"/signing/"+slot+"/sign", token, body, ct)
	}
	values, _ := json.Marshal(map[string]string{"name": "Иван Петров"})
	if r := sign(memberSlot, "member", map[string]string{"values": string(values), "pin": "123456"}, nil); r.Code != 409 || r.reason() != "certificate_missing" {
		t.Fatalf("no certificate: %d %s", r.Code, r.Body)
	}
	if _, err := e.me.Setup(context.Background(), authz.User(tenantA, "member", nil), "123456"); err != nil {
		t.Fatal(err)
	}
	if r := sign(memberSlot, "member", map[string]string{"values": "{not json", "pin": "123456"}, nil); r.Code != 422 {
		t.Fatalf("bad values json: %d %s", r.Code, r.Body)
	}
	if r := sign(memberSlot, "member", map[string]string{"values": string(values), "pin": "123456", "extra": "x"}, nil); r.Code != 400 {
		t.Fatalf("unknown text part: %d %s", r.Code, r.Body)
	}
	if r := sign(memberSlot, "member", map[string]string{"values": string(values), "pin": "000000"}, nil); r.Code != 403 || r.reason() != "pin_invalid" {
		t.Fatalf("wrong pin: %d %s", r.Code, r.Body)
	}
	if r := sign(memberSlot, "member", map[string]string{"values": "{}", "pin": "123456"}, nil); r.Code != 422 || r.reason() != "missing_required" {
		t.Fatalf("missing required: %d %s", r.Code, r.Body)
	}
	r = sign(memberSlot, "member", map[string]string{"values": string(values), "pin": "123456"},
		map[string][]byte{"signature": pngData(), "upload.photo": pngData()})
	expect(t, r, 200, "sign")
	if r.json(t)["status"] != "signed" || r.json(t)["submission_status"] != "in_progress" {
		t.Fatalf("sign %s", r.Body)
	}
	if r := sign(adminSlot, "admin", map[string]string{"pin": "123456"}, map[string][]byte{"signature": bytes.Repeat([]byte{1}, 2<<20)}); r.Code != 413 {
		t.Fatalf("oversized signature part: %d %s", r.Code, r.Body)
	}

	// Views after the first signature.
	r = call(s, "GET", Prefix+"/submissions/"+id+"/events", "reader", nil, "")
	expect(t, r, 200, "events")
	if len(r.json(t)["items"].([]any)) < 4 {
		t.Fatalf("events %s", r.Body)
	}
	r = call(s, "GET", Prefix+"/submissions/"+id+"/document", "member", nil, "")
	expect(t, r, 200, "signed signer downloads the current version")
	if !strings.Contains(r.Header().Get("Content-Disposition"), "v1") {
		t.Fatalf("disposition %s", r.Header().Get("Content-Disposition"))
	}
	expect(t, call(s, "GET", Prefix+"/submissions/"+id+"/document?version=0", "admin", nil, ""), 200, "sender downloads v0")
	expect(t, call(s, "GET", Prefix+"/submissions/"+id+"/audit-trail", "admin", nil, ""), 404, "no audit trail yet")
	r = call(s, "GET", Prefix+"/users?q=riT", "admin", nil, "")
	expect(t, r, 200, "users")
	if u := r.json(t)["items"].([]any); len(u) != 1 || u[0].(map[string]any)["display_name"] != "Rita Reader" {
		t.Fatalf("users %s", r.Body)
	}
	expect(t, call(s, "GET", Prefix+"/users", "member", nil, ""), 403, "users need submissions:create")

	// Sender controls.
	expect(t, call(s, "POST", Prefix+"/submissions/"+id+"/signers/"+adminSlot+"/resend", "admin", nil, ""), 200, "resend")
	r = callJSON(s, "PUT", Prefix+"/submissions/"+id+"/signers/"+adminSlot, "admin", map[string]any{"user_id": "reader"})
	expect(t, r, 200, "replace")
	r = call(s, "GET", Prefix+"/signing/"+adminSlot, "reader", nil, "")
	expect(t, r, 200, "replacement's session")
	r = callJSON(s, "POST", Prefix+"/signing/"+adminSlot+"/decline", "reader", map[string]any{"reason": "not my job"})
	expect(t, r, 200, "decline")
	if r.json(t)["submission_status"] != "cancelled" {
		t.Fatalf("decline %s", r.Body)
	}
	if r := callJSON(s, "POST", Prefix+"/submissions/"+id+"/cancel", "admin", map[string]any{"reason": "x"}); r.Code != 409 {
		t.Fatalf("cancel a cancelled submission: %d %s", r.Code, r.Body)
	}
	expect(t, call(s, "DELETE", Prefix+"/submissions/"+id, "member", nil, ""), 403, "signer cannot delete")
	expect(t, call(s, "DELETE", Prefix+"/submissions/"+id, "admin", nil, ""), 204, "delete")
	expect(t, call(s, "GET", Prefix+"/submissions/"+id, "admin", nil, ""), 404, "deleted")

	// Cancel through the API.
	id2, _, _ := e.create(t)
	r = callJSON(s, "POST", Prefix+"/submissions/"+id2+"/cancel", "admin", map[string]any{"reason": "typo"})
	expect(t, r, 200, "cancel")
	if r.json(t)["cancel_reason"] != "typo" {
		t.Fatalf("cancel %s", r.Body)
	}
}

func TestPackage(t *testing.T) {
	e := subsAPI(t)
	id, memberSlot, _ := e.create(t)
	expect(t, call(e.s, "POST", Prefix+"/submissions/"+id+"/send", "admin", nil, ""), 200, "send")
	expect(t, call(e.s, "GET", Prefix+"/submissions/"+id+"/package", "admin", nil, ""), 404, "unfinished")
	if _, err := e.me.Setup(context.Background(), authz.User(tenantA, "member", nil), "123456"); err != nil {
		t.Fatal(err)
	}
	values, _ := json.Marshal(map[string]string{"name": "Иван"})
	body, ct := multipartReq(map[string]string{"values": string(values), "pin": "123456"}, nil)
	expect(t, call(e.s, "POST", Prefix+"/signing/"+memberSlot+"/sign", "member", body, ct), 200, "sign")
	// Pretend the remaining signer and the audit-trail job are done.
	ctx := context.Background()
	sub, _, _ := e.mem.GetSubmission(ctx, tenantA, id)
	one := 1
	sub.Status, sub.FinalVersion, sub.AuditTrailKey = store.SubmissionCompleted, &one, blob.AuditTrail(tenantA, id)
	e.mem.PutSubmission(sub)
	expect(t, call(e.s, "GET", Prefix+"/submissions/"+id+"/package", "member", nil, ""), 503, "audit trail object missing")
	_, _ = e.blob.Put(ctx, sub.AuditTrailKey, bytes.NewReader([]byte("%PDF-trail")), 10, "application/pdf")
	r := call(e.s, "GET", Prefix+"/submissions/"+id+"/package", "member", nil, "")
	expect(t, r, 200, "package")
	zr, err := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
	if err != nil || len(zr.File) != 2 || zr.File[0].Name != "Договор.pdf" || zr.File[1].Name != "Договор - audit trail.pdf" {
		t.Fatalf("zip: %v %+v", err, zr)
	}
	if r.Header().Get("Content-Type") != "application/zip" {
		t.Fatal("content type")
	}
	expect(t, call(e.s, "GET", Prefix+"/submissions/"+id+"/package", "outsider", nil, ""), 404, "other tenant")
	if zipName("../a/b\\c") != "__a_b_c" {
		t.Fatalf("zipName %q", zipName("../a/b\\c"))
	}
}

func TestFileName(t *testing.T) {
	if fileName("  ", ".pdf") != "document.pdf" || fileName("Договор", " (v2).pdf") != "Договор (v2).pdf" {
		t.Fatal("fileName")
	}
}

func TestQESRoutes(t *testing.T) {
	e := subsAPI(t)
	id, memberSlot, _ := e.create(t)
	expect(t, call(e.s, "POST", Prefix+"/submissions/"+id+"/send", "admin", nil, ""), 200, "send")
	card := pdftest.Card("Ivan Card")
	chain := []string{base64.StdEncoding.EncodeToString(card.Chain[0].Raw), base64.StdEncoding.EncodeToString(card.Chain[1].Raw)}
	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData())
	if r := callJSON(e.s, "POST", Prefix+"/signing/"+memberSlot+"/qes/prepare", "member", map[string]any{
		"values": map[string]string{"name": "Иван"}, "chain": chain, "signature_image": "data:text/html;base64,PGI+"}); r.Code != 422 {
		t.Fatalf("bad image: %d %s", r.Code, r.Body)
	}
	r := callJSON(e.s, "POST", Prefix+"/signing/"+memberSlot+"/qes/prepare", "member", map[string]any{
		"values": map[string]string{"name": "Иван"}, "chain": chain, "signature_image": img})
	expect(t, r, 200, "prepare")
	p := r.json(t)
	if p["hash_algorithm"] != "SHA256" || p["signed_attrs_b64"] == "" || p["expires_at"] == "" {
		t.Fatalf("prepared %s", r.Body)
	}
	if _, ok := p["signed_contents_b64"]; ok {
		t.Fatal("no origin configured")
	}
	digest, _ := base64.StdEncoding.DecodeString(p["digest_b64"].(string))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, card.Key, crypto.SHA256, digest)
	expect(t, callJSON(e.s, "POST", Prefix+"/signing/"+memberSlot+"/qes/complete", "member", map[string]any{
		"preparation_id": p["preparation_id"], "signature_b64": "!!"}), 422, "bad base64")
	r = callJSON(e.s, "POST", Prefix+"/signing/"+memberSlot+"/qes/complete", "member", map[string]any{
		"preparation_id": p["preparation_id"], "signature_b64": base64.StdEncoding.EncodeToString(sig)})
	expect(t, r, 200, "complete")
	if r.json(t)["status"] != "signed" {
		t.Fatalf("complete %s", r.Body)
	}
	if b, err := dataURL(""); b != nil || err != nil {
		t.Fatal("empty data url")
	}
	if _, err := dataURL("data:image/png;base64,***"); err == nil {
		t.Fatal("bad base64 data url")
	}
}
