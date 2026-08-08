package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestScopesForSystemRoles_MinimumAndUnknownFailClosed(t *testing.T) {
	scopes, err := ScopesForSystemRoles([]string{model.SystemRoleUser, model.SystemRoleIAMAdmin})
	require.NoError(t, err)
	require.Equal(t, []string{
		model.ScopeAgentApprovalDecide,
		model.ScopeAgentApprovalRead,
		model.ScopeChatUse,
		model.ScopeIAMRolesRead,
		model.ScopeIAMRolesWrite,
		model.ScopeIAMUsersRead,
		model.ScopeIAMUsersWrite,
		model.ScopeProfileSelfRead,
	}, scopes)
	_, err = ScopesForSystemRoles([]string{"session-admin"})
	require.Error(t, err)
}

func TestIdentitySystemScopeAuthorizerUsesCurrentTenantState(t *testing.T) {
	state := activeAuthorization("tenant-a", "admin-1", 2, model.SystemRoleIAMAdmin)
	identityRepo := &testIdentityRepo{resolveFn: func(_ context.Context, tenantID, username string) (*repo.TenantAuthorization, error) {
		require.Equal(t, "tenant-a", tenantID)
		require.Equal(t, "admin-1", username)
		return state, nil
	}}
	authorizer := NewIdentitySystemScopeAuthorizer(identityRepo)

	allowed, err := authorizer.HasSystemScope(context.Background(), "tenant-a", "admin-1", model.ScopeAgentApprovalDecide)
	require.NoError(t, err)
	require.True(t, allowed)
	state.Membership.Status = model.TenantMembershipStatusDisabled
	allowed, err = authorizer.HasSystemScope(context.Background(), "tenant-a", "admin-1", model.ScopeAgentApprovalDecide)
	require.NoError(t, err)
	require.False(t, allowed)

	identityRepo.resolveFn = func(context.Context, string, string) (*repo.TenantAuthorization, error) {
		return nil, errors.New("database unavailable")
	}
	allowed, err = authorizer.HasSystemScope(context.Background(), "tenant-a", "admin-1", model.ScopeAgentApprovalDecide)
	require.Error(t, err)
	require.False(t, allowed)
}

func TestAuthService_LoginUsesTenantAuthorizationInToken(t *testing.T) {
	authenticator := newTestAuthenticator(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	require.NoError(t, err)
	userRepo := &testUserRepo{getUserByUsernameFn: func(context.Context, string) (*model.User, error) {
		return &model.User{Username: "alice", Password: string(hash), Kind: model.UserKindHuman}, nil
	}}
	identityRepo := &testIdentityRepo{resolveFn: func(_ context.Context, tenantID, username string) (*repo.TenantAuthorization, error) {
		require.Equal(t, "tenant-a", tenantID)
		require.Equal(t, "alice", username)
		return activeAuthorization(tenantID, username, 7, model.SystemRoleUser, model.SystemRoleIAMAdmin), nil
	}}
	service := NewAuthService(userRepo, identityRepo, &testSessionRepo{}, authenticator, testLogger())

	response, err := service.Login(context.Background(), &logicv1.LoginRequest{
		Username: "alice", Password: "correct-password", TenantId: "tenant-a",
	})
	require.NoError(t, err)
	require.Equal(t, "tenant-a", response.TenantId)
	require.Equal(t, []string{model.SystemRoleIAMAdmin, model.SystemRoleUser}, response.Roles)
	require.Contains(t, response.Scopes, model.ScopeIAMUsersWrite)

	claims, err := authenticator.ValidateAccessToken(context.Background(), response.AccessToken)
	require.NoError(t, err)
	require.Equal(t, "alice", claims.Subject)
	require.Equal(t, []string{model.SystemRoleIAMAdmin, model.SystemRoleUser}, claims.Roles)
	require.Equal(t, "tenant-a", claims.Extra["tenant_id"])
	require.Equal(t, "7", claims.Extra["membership_version"])
	require.ElementsMatch(t, stringSliceFromClaim(claims.Extra["scopes"]), response.Scopes)
}

func TestAuthService_ValidateTokenReloadsAuthoritativeRoles(t *testing.T) {
	authenticator := newTestAuthenticator(t)
	// Token 中的角色和 Scope 只是快照；ValidateToken 必须用 Repo 当前值覆盖。
	token, err := authenticator.GenerateTokenPair(context.Background(), &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "alice"},
		Roles:            []string{model.SystemRoleIAMAdmin},
		Extra: map[string]any{
			"tenant_id":          "tenant-a",
			"membership_version": "3",
			"scopes":             []string{model.ScopeIAMUsersWrite},
		},
	})
	require.NoError(t, err)
	userRepo := &testUserRepo{getUserByUsernameFn: func(context.Context, string) (*model.User, error) {
		return &model.User{Username: "alice", Kind: model.UserKindHuman}, nil
	}}
	identityRepo := &testIdentityRepo{resolveFn: func(context.Context, string, string) (*repo.TenantAuthorization, error) {
		return activeAuthorization("tenant-a", "alice", 3, model.SystemRoleUser), nil
	}}
	service := NewAuthService(userRepo, identityRepo, &testSessionRepo{}, authenticator, testLogger())

	response, err := service.ValidateToken(context.Background(), &logicv1.ValidateTokenRequest{AccessToken: token.AccessToken})
	require.NoError(t, err)
	require.True(t, response.Valid)
	require.Equal(t, []string{model.SystemRoleUser}, response.Roles)
	require.Equal(t, []string{model.ScopeChatUse, model.ScopeProfileSelfRead}, response.Scopes)
	require.NotContains(t, response.Scopes, model.ScopeIAMUsersWrite)
}

func TestAuthService_ValidateTokenFailsClosedOnIdentityState(t *testing.T) {
	authenticator := newTestAuthenticator(t)
	token, err := authenticator.GenerateTokenPair(context.Background(), &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "alice"},
		Extra: map[string]any{
			"tenant_id":          "tenant-a",
			"membership_version": "1",
			"scopes":             []string{model.ScopeChatUse},
		},
	})
	require.NoError(t, err)
	userRepo := &testUserRepo{getUserByUsernameFn: func(context.Context, string) (*model.User, error) {
		return &model.User{Username: "alice", Kind: model.UserKindHuman}, nil
	}}

	tests := []struct {
		name      string
		resolveFn func(context.Context, string, string) (*repo.TenantAuthorization, error)
	}{
		{name: "disabled membership", resolveFn: func(context.Context, string, string) (*repo.TenantAuthorization, error) {
			authorization := activeAuthorization("tenant-a", "alice", 1, model.SystemRoleUser)
			authorization.Membership.Status = model.TenantMembershipStatusDisabled
			return authorization, nil
		}},
		{name: "stale membership version", resolveFn: func(context.Context, string, string) (*repo.TenantAuthorization, error) {
			return activeAuthorization("tenant-a", "alice", 2, model.SystemRoleUser), nil
		}},
		{name: "repository outage", resolveFn: func(context.Context, string, string) (*repo.TenantAuthorization, error) {
			return nil, errors.New("database unavailable")
		}},
		{name: "unknown role", resolveFn: func(context.Context, string, string) (*repo.TenantAuthorization, error) {
			return activeAuthorization("tenant-a", "alice", 1, "session-admin"), nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewAuthService(userRepo, &testIdentityRepo{resolveFn: test.resolveFn}, &testSessionRepo{}, authenticator, testLogger())
			response, validateErr := service.ValidateToken(context.Background(), &logicv1.ValidateTokenRequest{AccessToken: token.AccessToken})
			require.NoError(t, validateErr)
			require.False(t, response.Valid)
		})
	}
}

func TestAuthService_RegisterCreatesDefaultTenantIdentityAtomically(t *testing.T) {
	authenticator := newTestAuthenticator(t)
	identityRepo := &testIdentityRepo{}
	sessionRepo := &testSessionRepo{getSessionFn: func(context.Context, string) (*model.Session, error) {
		return &model.Session{SessionID: "0", Name: "Resonance Room"}, nil
	}}
	service := NewAuthService(&testUserRepo{}, identityRepo, sessionRepo, authenticator, testLogger())

	response, err := service.Register(context.Background(), &logicv1.RegisterRequest{
		Username: "new-user", Password: "password", Nickname: "New User",
	})
	require.NoError(t, err)
	require.NotNil(t, identityRepo.createdUser)
	require.NotEqual(t, "password", identityRepo.createdUser.Password)
	require.Equal(t, model.DefaultTenantID, identityRepo.createdMembership.TenantID)
	require.Equal(t, model.TenantMembershipStatusActive, identityRepo.createdMembership.Status)
	require.Equal(t, []string{model.SystemRoleUser}, identityRepo.createdRoles)
	require.Equal(t, model.DefaultTenantID, response.TenantId)
	require.Equal(t, []string{model.SystemRoleUser}, response.Roles)
	require.Len(t, sessionRepo.addedMembers, 1)
}

func TestAuthService_ResolveUserPrincipalUsesAuthoritativeTenantState(t *testing.T) {
	userRepo := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		require.Equal(t, "alice", username)
		return &model.User{Username: "alice", Kind: model.UserKindHuman}, nil
	}}
	identityRepo := &testIdentityRepo{resolveFn: func(_ context.Context, tenantID, username string) (*repo.TenantAuthorization, error) {
		require.Equal(t, "tenant-a", tenantID)
		require.Equal(t, "alice", username)
		return activeAuthorization(tenantID, username, 9, model.SystemRoleUser, model.SystemRoleIAMAdmin), nil
	}}
	service := NewAuthService(userRepo, identityRepo, &testSessionRepo{}, newTestAuthenticator(t), testLogger())

	principal, err := service.ResolveUserPrincipal(context.Background(), "tenant-a", "alice")
	require.NoError(t, err)
	require.Equal(t, "tenant-a", principal.TenantID)
	require.Equal(t, "alice", principal.Username)
	require.Equal(t, int64(9), principal.Version)
	require.Equal(t, []string{model.SystemRoleIAMAdmin, model.SystemRoleUser}, principal.Roles)
	require.Contains(t, principal.Scopes, model.ScopeIAMUsersWrite)
}

func TestAuthService_ResolveUserPrincipalFailsClosedOnDisabledMembership(t *testing.T) {
	userRepo := &testUserRepo{getUserByUsernameFn: func(context.Context, string) (*model.User, error) {
		return &model.User{Username: "alice", Kind: model.UserKindHuman}, nil
	}}
	identityRepo := &testIdentityRepo{resolveFn: func(context.Context, string, string) (*repo.TenantAuthorization, error) {
		authorization := activeAuthorization("tenant-a", "alice", 2, model.SystemRoleUser)
		authorization.Membership.Status = model.TenantMembershipStatusDisabled
		return authorization, nil
	}}
	service := NewAuthService(userRepo, identityRepo, &testSessionRepo{}, newTestAuthenticator(t), testLogger())

	principal, err := service.ResolveUserPrincipal(context.Background(), "tenant-a", "alice")
	require.Error(t, err)
	require.Nil(t, principal)
}

func newTestAuthenticator(t *testing.T) auth.Authenticator {
	t.Helper()
	authenticator, err := auth.New(&auth.Config{
		SecretKey:      "identity-tests-secret-key-32-chars",
		Issuer:         "resonance-test",
		AccessTokenTTL: time.Hour,
	}, auth.WithLogger(testLogger()))
	require.NoError(t, err)
	return authenticator
}

func activeAuthorization(tenantID, username string, version int64, roles ...string) *repo.TenantAuthorization {
	return &repo.TenantAuthorization{
		Membership: &model.TenantMembership{
			TenantID: tenantID,
			Username: username,
			Status:   model.TenantMembershipStatusActive,
			Version:  version,
		},
		Roles: append([]string(nil), roles...),
	}
}

func stringSliceFromClaim(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

type testIdentityRepo struct {
	resolveFn         func(context.Context, string, string) (*repo.TenantAuthorization, error)
	createdUser       *model.User
	createdMembership *model.TenantMembership
	createdRoles      []string
}

func (r *testIdentityRepo) CreateIdentity(_ context.Context, user *model.User, membership *model.TenantMembership, roles []string) error {
	r.createdUser = user
	r.createdMembership = membership
	r.createdRoles = append([]string(nil), roles...)
	return nil
}

func (r *testIdentityRepo) CreateTenantMembership(context.Context, *model.TenantMembership) error {
	return nil
}

func (r *testIdentityRepo) GetTenantMembership(context.Context, string, string) (*model.TenantMembership, error) {
	return nil, repo.ErrTenantMembershipNotFound
}

func (r *testIdentityRepo) UpdateTenantMembershipStatus(context.Context, string, string, string, int64) (*model.TenantMembership, error) {
	return nil, repo.ErrTenantMembershipNotFound
}

func (r *testIdentityRepo) CreateSystemRoleBinding(context.Context, *model.SystemRoleBinding) error {
	return nil
}

func (r *testIdentityRepo) DeleteSystemRoleBinding(context.Context, string, string, string) error {
	return nil
}

func (r *testIdentityRepo) ListSystemRoleBindings(context.Context, string, string) ([]*model.SystemRoleBinding, error) {
	return nil, nil
}

func (r *testIdentityRepo) ListTenantMemberships(context.Context, string, int) ([]*model.TenantMembership, error) {
	return nil, nil
}

func (r *testIdentityRepo) ResolveTenantAuthorization(ctx context.Context, tenantID, username string) (*repo.TenantAuthorization, error) {
	if r.resolveFn == nil {
		return nil, repo.ErrTenantMembershipNotFound
	}
	return r.resolveFn(ctx, tenantID, username)
}

func (r *testIdentityRepo) Close() error { return nil }
