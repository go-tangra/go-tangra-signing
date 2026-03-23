package service

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	adminstubpb "github.com/go-tangra/go-tangra-common/gen/go/common/admin_stub/v1"
	"github.com/go-tangra/go-tangra-signing/internal/client"

	signingpb "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

// UserService wraps AdminClient to provide user listing for the signing module
type UserService struct {
	signingpb.UnimplementedSigningUserServiceServer

	log         *log.Helper
	adminClient *client.AdminClient
}

// NewUserService creates a new UserService
func NewUserService(ctx *bootstrap.Context, adminClient *client.AdminClient) *UserService {
	return &UserService{
		log:         ctx.NewLoggerHelper("signing/service/user"),
		adminClient: adminClient,
	}
}

// ListUsers fetches users from admin-service, filters out PENDING users, and maps to signing proto types
func (s *UserService) ListUsers(ctx context.Context, _ *signingpb.ListSigningUsersRequest) (*signingpb.ListSigningUsersResponse, error) {
	resp, err := s.adminClient.ListUsers(ctx)
	if err != nil {
		s.log.Errorf("Failed to list users from admin-service: %v", err)
		return nil, err
	}

	items := make([]*signingpb.SigningUser, 0, len(resp.Items))
	for _, u := range resp.Items {
		if u.Status != nil && u.GetStatus() == adminstubpb.AdminUser_PENDING {
			continue
		}
		items = append(items, &signingpb.SigningUser{
			Id:       u.Id,
			Username: u.Username,
			Realname: u.Realname,
			Email:    u.Email,
		})
	}

	return &signingpb.ListSigningUsersResponse{
		Items: items,
		Total: int32(len(items)),
	}, nil
}
