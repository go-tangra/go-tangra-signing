// Package signingclient is the module-side client of the signing module's
// signing.v1.ModuleSubmissions API (feature 028). It talks to the module over
// the Freya SPIFFE mTLS gRPC channel; New takes an already-dialed connection
// (e.g. freya.App.Client(ctx, "signing")), so an in-process bufconn server is
// enough to test it.
package signingclient

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	signingv1 "github.com/go-tangra/go-tangra-signing/sdk/v4/api/proto/signing/v1"
)

// Errors mapped from the module's gRPC codes.
var (
	ErrNotFound     = errors.New("signingclient: not found")
	ErrInvalid      = errors.New("signingclient: invalid request")
	ErrPrecondition = errors.New("signingclient: precondition failed") // template not active, signer inactive, not completed
	ErrDenied       = errors.New("signingclient: permission denied")
	ErrUnavailable  = errors.New("signingclient: unavailable")
)

// Error carries the mapped sentinel and the module's reason (the status
// message: a stable code such as template_not_active or signer_inactive).
type Error struct {
	Kind   error
	Reason string
}

func (e *Error) Error() string { return e.Kind.Error() + ": " + e.Reason }

// Unwrap returns the sentinel.
func (e *Error) Unwrap() error { return e.Kind }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return &Error{Kind: ErrUnavailable, Reason: "transport"}
	}
	kind := ErrUnavailable
	switch st.Code() {
	case codes.NotFound:
		kind = ErrNotFound
	case codes.InvalidArgument:
		kind = ErrInvalid
	case codes.FailedPrecondition:
		kind = ErrPrecondition
	case codes.PermissionDenied, codes.Unauthenticated:
		kind = ErrDenied
	}
	return &Error{Kind: kind, Reason: st.Message()}
}

// Client wraps the generated client.
type Client struct {
	c signingv1.ModuleSubmissionsClient
}

// New builds a Client over an established connection.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{c: signingv1.NewModuleSubmissionsClient(conn)}
}

// Party is a template party.
type Party struct{ ID, Name string }

// Field is a template field.
type Field struct {
	ID, Name, Type, Party string
	TextValued            bool
}

// Template is an active template with its parties and fields.
type Template struct {
	ID, Name string
	Parties  []Party
	Fields   []Field
}

// ListTemplates lists the tenant's active templates.
func (c *Client) ListTemplates(ctx context.Context, tenantID string) ([]Template, error) {
	res, err := c.c.ListTemplates(ctx, &signingv1.ListTemplatesRequest{TenantId: tenantID})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Template, 0, len(res.GetTemplates()))
	for _, t := range res.GetTemplates() {
		tpl := Template{ID: t.GetId(), Name: t.GetName()}
		for _, p := range t.GetParties() {
			tpl.Parties = append(tpl.Parties, Party{ID: p.GetId(), Name: p.GetName()})
		}
		for _, f := range t.GetFields() {
			tpl.Fields = append(tpl.Fields, Field{ID: f.GetId(), Name: f.GetName(), Type: f.GetType(), Party: f.GetParty(), TextValued: f.GetTextValued()})
		}
		out = append(out, tpl)
	}
	return out, nil
}

// Signer is one signer in signing order.
type Signer struct{ UserID, Party string }

// CreateInput creates and sends a submission.
type CreateInput struct {
	TenantID       string
	TemplateID     string
	SenderUserID   string
	Name           string
	Signers        []Signer
	Prefill        map[string]string
	SourceRef      string
	IdempotencyKey string
}

// CreateAndSend creates and sends a sequential submission; it returns the
// submission id.
func (c *Client) CreateAndSend(ctx context.Context, in CreateInput) (string, error) {
	req := &signingv1.CreateAndSendRequest{TenantId: in.TenantID, TemplateId: in.TemplateID, SenderUserId: in.SenderUserID, Name: in.Name,
		Prefill: in.Prefill, SourceRef: in.SourceRef, IdempotencyKey: in.IdempotencyKey}
	for _, s := range in.Signers {
		req.Signers = append(req.Signers, &signingv1.SignerRef{UserId: s.UserID, Party: s.Party})
	}
	res, err := c.c.CreateAndSend(ctx, req)
	if err != nil {
		return "", mapErr(err)
	}
	return res.GetSubmissionId(), nil
}

// State is a submission's state.
type State struct {
	Status           string // in_progress | completed | cancelled | expired
	CancelReasonCode string // cancelled | declined
	FinalVersion     int
}

// GetSubmission returns a submission's state.
func (c *Client) GetSubmission(ctx context.Context, tenantID, submissionID string) (State, error) {
	res, err := c.c.GetSubmission(ctx, &signingv1.GetSubmissionRequest{TenantId: tenantID, SubmissionId: submissionID})
	if err != nil {
		return State{}, mapErr(err)
	}
	return State{Status: res.GetStatus(), CancelReasonCode: res.GetCancelReasonCode(), FinalVersion: int(res.GetFinalVersion())}, nil
}

// Cancel cancels an in-progress submission.
func (c *Client) Cancel(ctx context.Context, tenantID, submissionID, reasonCode string) error {
	_, err := c.c.Cancel(ctx, &signingv1.CancelRequest{TenantId: tenantID, SubmissionId: submissionID, ReasonCode: reasonCode})
	return mapErr(err)
}

// Delete deletes a submission and its documents.
func (c *Client) Delete(ctx context.Context, tenantID, submissionID string) error {
	_, err := c.c.Delete(ctx, &signingv1.DeleteRequest{TenantId: tenantID, SubmissionId: submissionID})
	return mapErr(err)
}

// FinalDocument opens the signed PDF of a completed submission as a stream;
// the caller closes it (which cancels the call).
func (c *Client) FinalDocument(ctx context.Context, tenantID, submissionID string) (io.ReadCloser, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := c.c.FinalDocument(ctx, &signingv1.FinalDocumentRequest{TenantId: tenantID, SubmissionId: submissionID})
	if err != nil {
		cancel()
		return nil, "", mapErr(err)
	}
	first, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, "", mapErr(err)
	}
	return &reader{stream: stream, buf: first.GetData(), cancel: cancel}, first.GetFileName(), nil
}

type reader struct {
	stream grpc.ServerStreamingClient[signingv1.DocumentChunk]
	buf    []byte
	cancel context.CancelFunc
	err    error
}

func (r *reader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		chunk, err := r.stream.Recv()
		if errors.Is(err, io.EOF) {
			r.err = io.EOF
			continue
		}
		if err != nil {
			r.err = mapErr(err)
			continue
		}
		r.buf = chunk.GetData()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *reader) Close() error {
	r.cancel()
	return nil
}
