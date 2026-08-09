package service

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func (s *SessionService) requireUserPrincipal(ctx context.Context) (*UserPrincipal, error) {
	principal, ok := UserPrincipalFromCtx(ctx)
	if ok {
		return principal, nil
	}
	if s != nil && s.allowLegacy {
		username, err := MustUsernameFromCtx(ctx)
		if err == nil {
			return &UserPrincipal{TenantID: model.DefaultTenantID, Username: username, Version: 1}, nil
		}
	}
	return nil, status.Error(codes.Unauthenticated, "authenticated user principal is required")
}

func requireTrustedTenant(ctx context.Context) (string, error) {
	if principal, ok := UserPrincipalFromCtx(ctx); ok {
		return principal.TenantID, nil
	}
	if _, tenantID, ok := ServicePrincipalFromCtx(ctx); ok {
		return tenantID, nil
	}
	return "", status.Error(codes.Unauthenticated, "trusted tenant principal is required")
}

func requireSessionTenant(ctx context.Context, sessions repo.SessionRepo, sessionID string, allowLegacy ...bool) error {
	tenantID, err := requireTrustedTenant(ctx)
	if err != nil && len(allowLegacy) == 1 && allowLegacy[0] {
		if _, usernameErr := MustUsernameFromCtx(ctx); usernameErr == nil {
			tenantID = model.DefaultTenantID
			err = nil
		}
	}
	if err != nil {
		return err
	}
	session, err := sessions.GetSession(ctx, sessionID)
	if err != nil {
		return status.Error(codes.Internal, "failed to load session")
	}
	if session == nil || session.TenantID == "" || session.TenantID != tenantID {
		return status.Error(codes.PermissionDenied, "session belongs to another tenant")
	}
	if _, _, servicePrincipal := ServicePrincipalFromCtx(ctx); servicePrincipal {
		profile, ok := ServiceProfileFromCtx(ctx)
		if !ok || session.Kind != model.SessionKindAI || session.ProfileID != profile.ProfileID ||
			session.ProfileVersion != profile.ProfileVersion {
			return status.Error(codes.PermissionDenied, "service workload cannot access this session profile")
		}
	}
	return nil
}

func (s *SessionService) requireActiveTenantMember(ctx context.Context, tenantID, username string) error {
	if s.memberships == nil {
		return status.Error(codes.FailedPrecondition, "tenant membership authority is unavailable")
	}
	membership, err := s.memberships.GetTenantMembership(ctx, tenantID, username)
	if err != nil {
		if errors.Is(err, repo.ErrTenantMembershipNotFound) {
			return status.Error(codes.PermissionDenied, "user is not an active tenant member")
		}
		return status.Error(codes.Unavailable, "tenant membership authority is unavailable")
	}
	if membership == nil || membership.TenantID != tenantID || membership.Username != username ||
		membership.Status != model.TenantMembershipStatusActive || membership.Version < 1 {
		return status.Error(codes.PermissionDenied, "user is not an active tenant member")
	}
	return nil
}
