package grpcapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"
	signingv1 "github.com/go-tangra/go-tangra-signing/sdk/v4/api/proto/signing/v1"
	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/fieldvalues"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

const (
	tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	other  = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

var ctx = context.Background()

type env struct {
	client signingv1.ModuleSubmissionsClient
	mem    *memstore.Mem
	blob   *blob.Fake
	tpl    store.Template
	peer   *Peer
	subs   *submissions.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: blob.NewFake(), peer: &Peer{Service: "hr", SPIFFEID: "spiffe://example.org/svc/hr"}}
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "petar", DisplayName: "Petar", Email: "petar@example.org"},
		{UserID: "maria", DisplayName: "Maria", Email: "maria@example.org"},
	}}}
	kek := make([]byte, 32)
	sealer, err := sealed.NewEnvelope(kek)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	e.subs = submissions.New(submissions.Deps{Store: e.mem, Blob: e.blob, Checker: authz.Static{}, Contacts: dir,
		Mail: mail.Mailer{Sender: nopSender{}, PortalBaseURL: "https://portal.example.org"}, Events: events.Emitter{Pub: &events.Recorder{}},
		Now: func() time.Time { return now }, Values: fieldvalues.Box{E: sealer}})
	pdf := pdftest.Contract(1)
	id := store.NewID()
	key := blob.TemplatePDF(tenant, id, store.NewID())
	sum, _ := e.blob.Put(ctx, key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	e.tpl = store.Template{ID: id, TenantID: tenant, Name: "Leave form", Status: store.TemplateActive, PDFKey: key, PDFSHA256: sum,
		PDFSize: int64(len(pdf)), PDFPages: 1, Parties: []store.Party{{Key: "employee", Name: "Employee"}, {Key: "approver", Name: "Approver"}},
		Fields: []store.Field{{ID: "name", Name: "Name", Type: "text", Party: "employee", Page: 1, X: .1, Y: .1, W: .3, H: .03},
			{ID: "sig", Name: "Signature", Type: "signature", Party: "approver", Page: 1, X: .1, Y: .8, W: .3, H: .08}},
		Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := e.mem.CreateTemplate(ctx, e.tpl); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Subs: e.subs, ChunkSize: 1024, Caller: func(context.Context) (Peer, bool) {
		if e.peer == nil {
			return Peer{}, false
		}
		return *e.peer, true
	}}
	lis := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	signingv1.RegisterModuleSubmissionsServer(g, srv)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	e.client = signingv1.NewModuleSubmissionsClient(conn)
	return e
}

type nopSender struct{}

func (nopSender) SendKey(context.Context, string, string, string, map[string]string, string) (notifyclient.Result, error) {
	return notifyclient.Result{Sent: true}, nil
}

func code(err error) codes.Code { return status.Code(err) }

func (e *env) create(t *testing.T, key string) string {
	t.Helper()
	res, err := e.client.CreateAndSend(ctx, &signingv1.CreateAndSendRequest{TenantId: tenant, TemplateId: e.tpl.ID, SenderUserId: "petar",
		Name: "Annual leave", Signers: []*signingv1.SignerRef{{UserId: "maria", Party: "employee"}, {UserId: "petar", Party: "approver"}},
		Prefill: map[string]string{"name": "Maria"}, SourceRef: "req-1", IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return res.GetSubmissionId()
}

func TestModuleAPI(t *testing.T) {
	e := newEnv(t)
	ts, err := e.client.ListTemplates(ctx, &signingv1.ListTemplatesRequest{TenantId: tenant})
	if err != nil || len(ts.GetTemplates()) != 1 || len(ts.GetTemplates()[0].GetParties()) != 2 || !ts.GetTemplates()[0].GetFields()[0].GetTextValued() ||
		ts.GetTemplates()[0].GetFields()[1].GetTextValued() {
		t.Fatalf("templates: %+v %v", ts, err)
	}
	id := e.create(t, "req-1:1")
	if again := e.create(t, "req-1:1"); again != id {
		t.Fatal("idempotent replay")
	}
	st, err := e.client.GetSubmission(ctx, &signingv1.GetSubmissionRequest{TenantId: tenant, SubmissionId: id})
	if err != nil || st.GetStatus() != store.SubmissionInProgress {
		t.Fatalf("state: %+v %v", st, err)
	}
	if _, err := e.client.FinalDocument(ctx, &signingv1.FinalDocumentRequest{TenantId: tenant, SubmissionId: id}); err != nil {
		t.Fatal(err) // stream opens; the error arrives on Recv
	}
	stream, _ := e.client.FinalDocument(ctx, &signingv1.FinalDocumentRequest{TenantId: tenant, SubmissionId: id})
	if _, err := stream.Recv(); code(err) != codes.FailedPrecondition {
		t.Fatalf("document before completion: %v", err)
	}
	c, err := e.client.Cancel(ctx, &signingv1.CancelRequest{TenantId: tenant, SubmissionId: id, ReasonCode: "hr_cancelled"})
	if err != nil || c.GetStatus() != store.SubmissionCancelled {
		t.Fatal(err)
	}
	if _, err := e.client.Cancel(ctx, &signingv1.CancelRequest{TenantId: tenant, SubmissionId: id, ReasonCode: "hr_cancelled"}); code(err) != codes.FailedPrecondition {
		t.Fatalf("cancel twice: %v", err)
	}
	if _, err := e.client.Cancel(ctx, &signingv1.CancelRequest{TenantId: tenant, SubmissionId: id, ReasonCode: "?"}); code(err) != codes.InvalidArgument {
		t.Fatalf("bad reason: %v", err)
	}

	// Completed: the document streams in chunks with its name first.
	id2 := e.create(t, "req-2:1")
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, id2)
	zero := 0
	sub.Status, sub.FinalVersion = store.SubmissionCompleted, &zero
	_ = e.mem.UpdateSubmission(ctx, sub)
	stream, err = e.client.FinalDocument(ctx, &signingv1.FinalDocumentRequest{TenantId: tenant, SubmissionId: id2})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	first, err := stream.Recv()
	if err != nil || first.GetFileName() != "Annual leave.pdf" || first.GetSize() == 0 {
		t.Fatalf("first chunk: %+v %v", first, err)
	}
	got.Write(first.GetData())
	for {
		ch, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got.Write(ch.GetData())
	}
	if int64(got.Len()) != first.GetSize() {
		t.Fatalf("document size %d != %d", got.Len(), first.GetSize())
	}
	if _, err := e.client.Delete(ctx, &signingv1.DeleteRequest{TenantId: tenant, SubmissionId: id2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.GetSubmission(ctx, &signingv1.GetSubmissionRequest{TenantId: tenant, SubmissionId: id2}); code(err) != codes.NotFound {
		t.Fatalf("deleted: %v", err)
	}
	if _, err := e.client.Delete(ctx, &signingv1.DeleteRequest{TenantId: tenant, SubmissionId: id2}); code(err) != codes.NotFound {
		t.Fatal("delete twice")
	}
}

func TestModuleAPIRefusals(t *testing.T) {
	e := newEnv(t)
	id := e.create(t, "req-1:1")
	// Another tenant, a malformed tenant, a user's own submission.
	if _, err := e.client.GetSubmission(ctx, &signingv1.GetSubmissionRequest{TenantId: other, SubmissionId: id}); code(err) != codes.NotFound {
		t.Fatalf("foreign tenant: %v", err)
	}
	if _, err := e.client.ListTemplates(ctx, &signingv1.ListTemplatesRequest{TenantId: "x"}); code(err) != codes.InvalidArgument {
		t.Fatal("tenant id")
	}
	// Invalid creations.
	bad := []*signingv1.CreateAndSendRequest{
		{TenantId: tenant, TemplateId: e.tpl.ID, SenderUserId: "petar", IdempotencyKey: "k1"}, // no signers
		{TenantId: tenant, TemplateId: e.tpl.ID, SenderUserId: "ghost", IdempotencyKey: "k2", Signers: []*signingv1.SignerRef{{UserId: "maria", Party: "employee"}}},
		{TenantId: tenant, TemplateId: store.NewID(), SenderUserId: "petar", IdempotencyKey: "k3", Signers: []*signingv1.SignerRef{{UserId: "maria", Party: "employee"}}},
	}
	for i, r := range bad {
		if _, err := e.client.CreateAndSend(ctx, r); code(err) != codes.InvalidArgument && code(err) != codes.NotFound {
			t.Errorf("bad create %d: %v", i, err)
		}
	}
	// Other callers.
	e.peer = &Peer{Service: "ipam", SPIFFEID: "spiffe://example.org/svc/ipam"}
	for _, err := range []error{
		func() error {
			_, err := e.client.ListTemplates(ctx, &signingv1.ListTemplatesRequest{TenantId: tenant})
			return err
		}(),
		func() error {
			_, err := e.client.GetSubmission(ctx, &signingv1.GetSubmissionRequest{TenantId: tenant, SubmissionId: id})
			return err
		}(),
		func() error {
			_, err := e.client.CreateAndSend(ctx, &signingv1.CreateAndSendRequest{TenantId: tenant})
			return err
		}(),
		func() error { _, err := e.client.Cancel(ctx, &signingv1.CancelRequest{TenantId: tenant}); return err }(),
		func() error { _, err := e.client.Delete(ctx, &signingv1.DeleteRequest{TenantId: tenant}); return err }(),
		func() error {
			s, _ := e.client.FinalDocument(ctx, &signingv1.FinalDocumentRequest{TenantId: tenant})
			_, err := s.Recv()
			return err
		}(),
	} {
		if code(err) != codes.PermissionDenied {
			t.Fatalf("foreign service: %v", err)
		}
	}
	e.peer = nil
	if _, err := e.client.ListTemplates(ctx, &signingv1.ListTemplatesRequest{TenantId: tenant}); code(err) != codes.Unauthenticated {
		t.Fatal("no peer")
	}
	if _, _, err := (&Server{}).caller(ctx, tenant); code(err) != codes.Unauthenticated {
		t.Fatal("nil caller")
	}
	e.peer = &Peer{Service: "hr"}
	e.mem.Fail("ListTemplates", errors.New("db"))
	if _, err := e.client.ListTemplates(ctx, &signingv1.ListTemplatesRequest{TenantId: tenant}); code(err) != codes.Internal {
		t.Fatalf("store error: %v", err)
	}
	e.mem.Fail("", nil)
	for err, want := range map[error]codes.Code{
		apperr.TemporarilyUnavailable: codes.Unavailable, apperr.Forbidden: codes.PermissionDenied, apperr.TemplateNotActive: codes.FailedPrecondition,
		apperr.InvalidSigner: codes.InvalidArgument, errors.New("x"): codes.Internal, &apperr.Error{Status: 503, Reason: "odd"}: codes.Internal,
	} {
		if code(grpcError(err)) != want {
			t.Errorf("grpcError(%v) = %v", err, code(grpcError(err)))
		}
	}
}
