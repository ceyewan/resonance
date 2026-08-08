package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ceyewan/genesis/auth"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/pkg/userauth"
)

func TestWithPrincipalPreservesTenantAndMembershipVersion(t *testing.T) {
	original := &Principal{
		Username:          "alice",
		TenantID:          "tenant-a",
		MembershipVersion: 7,
	}
	ctx := WithPrincipal(context.Background(), original)
	original.MembershipVersion = 8

	username, ok := UsernameFromRequestContext(ctx)
	require.True(t, ok)
	require.Equal(t, "alice", username)
	principal, ok := PrincipalFromRequestContext(ctx)
	require.True(t, ok)
	require.Equal(t, "tenant-a", principal.TenantID)
	require.Equal(t, int64(7), principal.MembershipVersion)

	principal.MembershipVersion = 9
	again, ok := PrincipalFromRequestContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(7), again.MembershipVersion)
}

func TestAuthConfigValidatesJWTLocallyAndKeepsOnlyMinimalPrincipal(t *testing.T) {
	authenticator, err := auth.New(&auth.Config{
		SecretKey: "jwt-secret-0123456789abcdef012345",
		Issuer:    "resonance-service",
	})
	require.NoError(t, err)
	tokens, err := authenticator.GenerateTokenPair(context.Background(), &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "alice"},
		Username:         "untrusted-snapshot-name",
		Roles:            []string{"iam-admin"},
		Extra: map[string]any{
			"tenant_id":          "tenant-a",
			"membership_version": "7",
			"scopes":             []string{"iam:users:write"},
		},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ginCtx.Request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	config := NewAuthConfig(authenticator, nil)
	principal, err := config.extractAndValidate(ginCtx)
	require.NoError(t, err)
	require.Equal(t, &Principal{
		Username: "alice", TenantID: "tenant-a", MembershipVersion: 7,
	}, principal)

	ctx := userauth.WithPrincipal(context.Background(), &userauth.Principal{
		Username: principal.Username, TenantID: principal.TenantID, MembershipVersion: principal.MembershipVersion,
	})
	minimal, ok := userauth.PrincipalFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "alice", minimal.Username)
	require.Equal(t, int64(7), minimal.MembershipVersion)
}

func TestAuthConfigRejectsSignedJWTWithoutTenantMembershipVersion(t *testing.T) {
	authenticator, err := auth.New(&auth.Config{SecretKey: "jwt-secret-0123456789abcdef012345"})
	require.NoError(t, err)
	tokens, err := authenticator.GenerateTokenPair(context.Background(), &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "alice"},
		Extra:            map[string]any{"tenant_id": "tenant-a"},
	})
	require.NoError(t, err)

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ginCtx.Request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	_, err = NewAuthConfig(authenticator, nil).extractAndValidate(ginCtx)
	require.ErrorIs(t, err, ErrInvalidToken)
}
