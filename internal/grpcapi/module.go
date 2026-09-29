// Package grpcapi serves the signing module's module-to-module API,
// signing.v1.ModuleSubmissions (feature 028), on the Freya SPIFFE mTLS gRPC
// server. The mesh policy admits only the allowed services; the handlers
// re-check the peer (defence in depth): it must belong to signing's trust
// domain and its service name must be an allowed source. The tenant comes from
// the request; submissions are scoped to the caller's source.
package grpcapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	signingv1 "github.com/go-tangra/go-tangra-signing/sdk/v4/api/proto/signing/v1"
	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

// Sources maps a caller's service name to its submission source.
var Sources = map[string]string{"hr": store.SourceHR}

// Peer is a verified mesh caller.
type Peer struct {
	Service  string // e.g. "hr"
	SPIFFEID string
}

// Server implements signing.v1.ModuleSubmissions.
type Server struct {
	signingv1.UnimplementedModuleSubmissionsServer
	Subs *submissions.Service
	// Caller returns the verified peer of the call (nil: no peer accepted).
	Caller func(ctx context.Context) (Peer, bool)
	// ChunkSize of FinalDocument (default 64 KiB).
	ChunkSize int
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// caller resolves the source of the call and checks the tenant id.
func (s *Server) caller(ctx context.Context, tenantID string) (Peer, string, error) {
	if s.Caller == nil {
		return Peer{}, "", status.Error(codes.Unauthenticated, "service identity required")
	}
	p, ok := s.Caller(ctx)
	if !ok {
		return Peer{}, "", status.Error(codes.Unauthenticated, "service identity required")
	}
	source, ok := Sources[p.Service]
	if !ok {
		return Peer{}, "", status.Error(codes.PermissionDenied, "caller not allowed")
	}
	if !uuidRE.MatchString(tenantID) {
		return Peer{}, "", status.Error(codes.InvalidArgument, "tenant_id")
	}
	return p, source, nil
}

// grpcError maps a service error to a gRPC status whose message is the
// stable reason code.
func grpcError(err error) error {
	if e, ok := apperr.As(err); ok {
		switch {
		case errors.Is(e, apperr.NotFound):
			return status.Error(codes.NotFound, e.Reason)
		case errors.Is(e, apperr.TemplateNotActive), errors.Is(e, apperr.SubmissionNotOpen):
			return status.Error(codes.FailedPrecondition, e.Reason)
		case errors.Is(e, apperr.TemporarilyUnavailable):
			return status.Error(codes.Unavailable, e.Reason)
		case errors.Is(e, apperr.Forbidden):
			return status.Error(codes.PermissionDenied, e.Reason)
		case e.Status >= 400 && e.Status < 500:
			return status.Error(codes.InvalidArgument, e.Reason)
		}
	}
	return status.Error(codes.Internal, "internal")
}

// ListTemplates implements signing.v1.ModuleSubmissions.
func (s *Server) ListTemplates(ctx context.Context, r *signingv1.ListTemplatesRequest) (*signingv1.ListTemplatesResponse, error) {
	if _, _, err := s.caller(ctx, r.GetTenantId()); err != nil {
		return nil, err
	}
	ts, err := s.Subs.ModuleTemplates(ctx, r.GetTenantId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &signingv1.ListTemplatesResponse{}
	for _, t := range ts {
		ti := &signingv1.TemplateInfo{Id: t.ID, Name: t.Name}
		for _, p := range t.Parties {
			ti.Parties = append(ti.Parties, &signingv1.TemplateParty{Id: p.Key, Name: p.Name})
		}
		for _, f := range t.Fields {
			ti.Fields = append(ti.Fields, &signingv1.TemplateField{Id: f.ID, Name: f.Name, Type: f.Type, Party: f.Party,
				TextValued: submissions.TextValued(f.Type)})
		}
		out.Templates = append(out.Templates, ti)
	}
	return out, nil
}

// CreateAndSend implements signing.v1.ModuleSubmissions.
func (s *Server) CreateAndSend(ctx context.Context, r *signingv1.CreateAndSendRequest) (*signingv1.CreateAndSendResponse, error) {
	p, source, err := s.caller(ctx, r.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := submissions.ModuleInput{TemplateID: r.GetTemplateId(), SenderUserID: r.GetSenderUserId(), Name: r.GetName(),
		Prefill: r.GetPrefill(), SourceRef: r.GetSourceRef(), IdempotencyKey: r.GetIdempotencyKey()}
	for _, sg := range r.GetSigners() {
		in.Signers = append(in.Signers, submissions.ModuleSigner{UserID: sg.GetUserId(), Party: sg.GetParty()})
	}
	sub, err := s.Subs.ModuleCreateAndSend(ctx, r.GetTenantId(), p.SPIFFEID, source, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return &signingv1.CreateAndSendResponse{SubmissionId: sub.ID, Status: sub.Status}, nil
}

// GetSubmission implements signing.v1.ModuleSubmissions.
func (s *Server) GetSubmission(ctx context.Context, r *signingv1.GetSubmissionRequest) (*signingv1.GetSubmissionResponse, error) {
	_, source, err := s.caller(ctx, r.GetTenantId())
	if err != nil {
		return nil, err
	}
	st, err := s.Subs.ModuleState(ctx, r.GetTenantId(), source, r.GetSubmissionId())
	if err != nil {
		return nil, grpcError(err)
	}
	return &signingv1.GetSubmissionResponse{SubmissionId: st.ID, Status: st.Status, CancelReasonCode: st.CancelReasonCode,
		FinalVersion: int32(st.FinalVersion)}, nil // #nosec G115 -- versions are small counters
}

// Cancel implements signing.v1.ModuleSubmissions.
func (s *Server) Cancel(ctx context.Context, r *signingv1.CancelRequest) (*signingv1.CancelResponse, error) {
	p, source, err := s.caller(ctx, r.GetTenantId())
	if err != nil {
		return nil, err
	}
	st, err := s.Subs.ModuleCancel(ctx, r.GetTenantId(), p.SPIFFEID, source, r.GetSubmissionId(), r.GetReasonCode())
	if err != nil {
		return nil, grpcError(err)
	}
	return &signingv1.CancelResponse{Status: st.Status}, nil
}

// Delete implements signing.v1.ModuleSubmissions.
func (s *Server) Delete(ctx context.Context, r *signingv1.DeleteRequest) (*signingv1.DeleteResponse, error) {
	p, source, err := s.caller(ctx, r.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.Subs.ModuleDelete(ctx, r.GetTenantId(), p.SPIFFEID, source, r.GetSubmissionId()); err != nil {
		return nil, grpcError(err)
	}
	return &signingv1.DeleteResponse{}, nil
}

// FinalDocument implements signing.v1.ModuleSubmissions.
func (s *Server) FinalDocument(r *signingv1.FinalDocumentRequest, stream signingv1.ModuleSubmissions_FinalDocumentServer) error {
	ctx := stream.Context()
	_, source, err := s.caller(ctx, r.GetTenantId())
	if err != nil {
		return err
	}
	rc, v, sub, err := s.Subs.ModuleDocument(ctx, r.GetTenantId(), source, r.GetSubmissionId())
	if err != nil {
		return grpcError(err)
	}
	defer func() { _ = rc.Close() }()
	size := s.ChunkSize
	if size <= 0 {
		size = 64 << 10
	}
	buf := make([]byte, size)
	first := true
	for {
		n, rerr := io.ReadFull(rc, buf)
		if n > 0 || first {
			chunk := &signingv1.DocumentChunk{Data: bytes.Clone(buf[:n])}
			if first {
				chunk.FileName, chunk.Size, first = sub.Name+".pdf", v.Size, false
			}
			if err := stream.Send(chunk); err != nil {
				return err
			}
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			return nil
		}
		if rerr != nil {
			return status.Error(codes.Internal, "read")
		}
	}
}
