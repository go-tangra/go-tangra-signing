package signingclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	signingv1 "github.com/go-tangra/go-tangra-signing/sdk/v4/api/proto/signing/v1"
)

const tn = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

type fake struct {
	signingv1.UnimplementedModuleSubmissionsServer
	err     error
	lastReq *signingv1.CreateAndSendRequest
	doc     []byte
	midErr  bool
}

func (f *fake) ListTemplates(context.Context, *signingv1.ListTemplatesRequest) (*signingv1.ListTemplatesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &signingv1.ListTemplatesResponse{Templates: []*signingv1.TemplateInfo{{Id: "t1", Name: "Leave",
		Parties: []*signingv1.TemplateParty{{Id: "p1", Name: "Employee"}},
		Fields:  []*signingv1.TemplateField{{Id: "f1", Name: "Name", Type: "text", Party: "p1", TextValued: true}}}}}, nil
}

func (f *fake) CreateAndSend(_ context.Context, r *signingv1.CreateAndSendRequest) (*signingv1.CreateAndSendResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastReq = r
	return &signingv1.CreateAndSendResponse{SubmissionId: "s1", Status: "in_progress"}, nil
}

func (f *fake) GetSubmission(context.Context, *signingv1.GetSubmissionRequest) (*signingv1.GetSubmissionResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &signingv1.GetSubmissionResponse{SubmissionId: "s1", Status: "cancelled", CancelReasonCode: "declined", FinalVersion: 2}, nil
}

func (f *fake) Cancel(context.Context, *signingv1.CancelRequest) (*signingv1.CancelResponse, error) {
	return &signingv1.CancelResponse{Status: "cancelled"}, f.err
}

func (f *fake) Delete(context.Context, *signingv1.DeleteRequest) (*signingv1.DeleteResponse, error) {
	return &signingv1.DeleteResponse{}, f.err
}

func (f *fake) FinalDocument(_ *signingv1.FinalDocumentRequest, s grpc.ServerStreamingServer[signingv1.DocumentChunk]) error {
	if f.err != nil {
		return f.err
	}
	for i := 0; i < len(f.doc); i += 3 {
		c := &signingv1.DocumentChunk{Data: f.doc[i:min(i+3, len(f.doc))]}
		if i == 0 {
			c.FileName, c.Size = "leave.pdf", int64(len(f.doc))
		}
		if err := s.Send(c); err != nil {
			return err
		}
		if f.midErr && i > 0 {
			return status.Error(codes.Internal, "broken")
		}
	}
	return nil
}

func client(t *testing.T, f *fake) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	signingv1.RegisterModuleSubmissionsServer(g, f)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestClient(t *testing.T) {
	f := &fake{doc: []byte("%PDF-1.7 signed")}
	c := client(t, f)
	ctx := context.Background()
	ts, err := c.ListTemplates(ctx, tn)
	if err != nil || len(ts) != 1 || ts[0].Parties[0].Name != "Employee" || !ts[0].Fields[0].TextValued {
		t.Fatalf("templates: %+v %v", ts, err)
	}
	id, err := c.CreateAndSend(ctx, CreateInput{TenantID: tn, TemplateID: "t1", SenderUserID: "petar", Name: "Leave",
		Signers: []Signer{{UserID: "maria", Party: "p1"}, {UserID: "petar", Party: "p2"}}, Prefill: map[string]string{"f1": "Maria"},
		SourceRef: "r1", IdempotencyKey: "r1:1"})
	if err != nil || id != "s1" || len(f.lastReq.Signers) != 2 || f.lastReq.Prefill["f1"] != "Maria" || f.lastReq.IdempotencyKey != "r1:1" {
		t.Fatalf("create: %v %+v", err, f.lastReq)
	}
	st, err := c.GetSubmission(ctx, tn, "s1")
	if err != nil || st.Status != "cancelled" || st.CancelReasonCode != "declined" || st.FinalVersion != 2 {
		t.Fatalf("state: %+v %v", st, err)
	}
	if err := c.Cancel(ctx, tn, "s1", "hr_cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, tn, "s1"); err != nil {
		t.Fatal(err)
	}
	rc, name, err := c.FinalDocument(ctx, tn, "s1")
	if err != nil || name != "leave.pdf" {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(b, f.doc) {
		t.Fatalf("doc: %q %v", b, err)
	}
	_ = rc.Close()
	f.midErr = true
	rc, _, err = c.FinalDocument(ctx, tn, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err == nil {
		t.Fatal("broken stream accepted")
	}
	_ = rc.Close()
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	cases := map[codes.Code]error{codes.NotFound: ErrNotFound, codes.InvalidArgument: ErrInvalid, codes.FailedPrecondition: ErrPrecondition,
		codes.PermissionDenied: ErrDenied, codes.Unauthenticated: ErrDenied, codes.Unavailable: ErrUnavailable}
	for code, want := range cases {
		f := &fake{err: status.Error(code, "template_not_active")}
		c := client(t, f)
		_, err := c.ListTemplates(ctx, tn)
		var e *Error
		if !errors.Is(err, want) || !errors.As(err, &e) || e.Reason != "template_not_active" || e.Error() == "" {
			t.Errorf("%v: %v", code, err)
		}
		if _, err := c.CreateAndSend(ctx, CreateInput{}); !errors.Is(err, want) {
			t.Errorf("create %v", code)
		}
		if _, err := c.GetSubmission(ctx, tn, "s"); !errors.Is(err, want) {
			t.Errorf("get %v", code)
		}
		if err := c.Cancel(ctx, tn, "s", ""); !errors.Is(err, want) {
			t.Errorf("cancel %v", code)
		}
		if err := c.Delete(ctx, tn, "s"); !errors.Is(err, want) {
			t.Errorf("delete %v", code)
		}
		if _, _, err := c.FinalDocument(ctx, tn, "s"); !errors.Is(err, want) {
			t.Errorf("document %v", code)
		}
	}
	if err := mapErr(errors.New("plain")); !errors.Is(err, ErrUnavailable) {
		t.Fatal("non-status error")
	}
	// A dial that never connects fails the call.
	conn, _ := grpc.NewClient("passthrough:///nowhere", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("no route")
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := New(conn).FinalDocument(cctx, tn, "s"); err == nil {
		t.Fatal("dead connection")
	}
}
