package service

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
)

// GetContactList 实现 SessionService.GetContactList
func (s *SessionService) GetContactList(ctx context.Context, req *logicv1.GetContactListRequest) (*logicv1.GetContactListResponse, error) {
	principal, err := s.requireUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}

	contacts, err := s.sessionRepo.GetContactListByTenant(ctx, principal.TenantID, principal.Username)
	if err != nil {
		s.logger.Error("failed to get contacts", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get contacts")
	}

	contactInfos := make([]*commonv1.ContactInfo, 0, len(contacts))
	for _, c := range contacts {
		if c == nil || c.Kind != model.UserKindHuman {
			continue
		}
		if err := s.requireActiveTenantMember(ctx, principal.TenantID, c.Username); err != nil {
			continue
		}
		contactInfos = append(contactInfos, &commonv1.ContactInfo{
			Username:  c.Username,
			Nickname:  c.Nickname,
			AvatarUrl: c.Avatar,
		})
	}

	return &logicv1.GetContactListResponse{Contacts: contactInfos}, nil
}

// SearchUser 实现 SessionService.SearchUser
func (s *SessionService) SearchUser(ctx context.Context, req *logicv1.SearchUserRequest) (*logicv1.SearchUserResponse, error) {
	principal, err := s.requireUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}

	users, err := s.userRepo.SearchUsers(ctx, req.Query)
	if err != nil {
		s.logger.Error("failed to search users", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to search users")
	}

	contacts := make([]*commonv1.ContactInfo, 0, len(users))
	for _, u := range users {
		if u == nil || u.Kind != model.UserKindHuman {
			continue
		}
		if err := s.requireActiveTenantMember(ctx, principal.TenantID, u.Username); err != nil {
			continue
		}
		contacts = append(contacts, &commonv1.ContactInfo{
			Username:  u.Username,
			Nickname:  u.Nickname,
			AvatarUrl: u.Avatar,
		})
	}

	return &logicv1.SearchUserResponse{Users: contacts}, nil
}
