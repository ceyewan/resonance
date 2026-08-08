package logicclient

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pkg/serviceauth"
	"github.com/ceyewan/resonance/pkg/userauth"
)

func TestUserServiceAuthUnaryInterceptorSignsPayloadBoundPrincipal(t *testing.T) {
	now := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)
	secret := []byte("gateway-secret-0123456789abcdef01")
	signer, err := serviceauth.NewSigner("gateway-service", secret,
		serviceauth.WithSignerClock(func() time.Time { return now }),
		serviceauth.WithNonceSource(func() (string, error) { return "55555555555555555555555555555555", nil }),
	)
	require.NoError(t, err)
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]serviceauth.ServicePolicy{
			"gateway-service": {
				Secret:                  secret,
				AllowAnyActor:           true,
				AllowAnyMethod:          true,
				AllowAnyTenant:          true,
				RequirePrincipalVersion: true,
			},
		},
	}, serviceauth.WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)

	request := &logicv1.CreateSessionRequest{Name: "room"}
	ctx := userauth.WithPrincipal(context.Background(), &userauth.Principal{
		TenantID: "tenant-a", Username: "alice", MembershipVersion: 7,
	})
	called := false
	err = userServiceAuthUnaryInterceptor(signer)(
		ctx,
		logicv1.SessionService_CreateSession_FullMethodName,
		request,
		&logicv1.CreateSessionResponse{},
		nil,
		func(ctx context.Context, method string, req, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			called = true
			outgoing, ok := metadata.FromOutgoingContext(ctx)
			require.True(t, ok)
			incoming := metadata.NewIncomingContext(context.Background(), outgoing.Copy())
			claims, verifyErr := verifier.VerifyIncoming(incoming, method, req.(*logicv1.CreateSessionRequest))
			require.NoError(t, verifyErr)
			require.Equal(t, "gateway-service", claims.ServiceID)
			require.Equal(t, "tenant-a", claims.TenantID)
			require.Equal(t, "alice", claims.Actor)
			require.Equal(t, int64(7), claims.PrincipalVersion)
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, called)
}

func TestUserServiceAuthUnaryInterceptorDoesNotSignPublicCalls(t *testing.T) {
	called := false
	err := userServiceAuthUnaryInterceptor(nil)(
		context.Background(),
		logicv1.AuthService_Login_FullMethodName,
		&logicv1.LoginRequest{},
		&logicv1.LoginResponse{},
		nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			called = true
			outgoing, ok := metadata.FromOutgoingContext(ctx)
			require.False(t, ok)
			require.Empty(t, outgoing)
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, called)
}

func TestUserServiceAuthUnaryInterceptorFailsClosedWithoutSigner(t *testing.T) {
	ctx := userauth.WithPrincipal(context.Background(), &userauth.Principal{
		TenantID: "tenant-a", Username: "alice", MembershipVersion: 1,
	})
	called := false
	err := userServiceAuthUnaryInterceptor(nil)(
		ctx,
		logicv1.SessionService_CreateSession_FullMethodName,
		&logicv1.CreateSessionRequest{},
		&logicv1.CreateSessionResponse{},
		nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			called = true
			return nil
		},
	)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestUserServiceAuthUnaryInterceptorBindsApprovalPayloadToPrincipalTenant(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	secret := []byte("gateway-approval-secret-0123456789")
	signer, err := serviceauth.NewSigner("gateway-service", secret,
		serviceauth.WithSignerClock(func() time.Time { return now }),
		serviceauth.WithNonceSource(func() (string, error) { return "77777777777777777777777777777777", nil }),
	)
	require.NoError(t, err)
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]serviceauth.ServicePolicy{
			"gateway-service": {
				Secret: secret, AllowedMethods: map[string]struct{}{
					logicv1.AgentApprovalService_DecideApproval_FullMethodName: {},
				},
				AllowAnyActor: true, AllowAnyTenant: true, RequirePrincipalVersion: true,
			},
		},
	}, serviceauth.WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)

	request := &logicv1.DecideApprovalRequest{
		TenantId: "tenant-a", CallId: "call-1", ArgsHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpectedVersion: 3, Decision: logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE,
	}
	ctx := userauth.WithPrincipal(context.Background(), &userauth.Principal{
		TenantID: "tenant-a", Username: "admin", MembershipVersion: 9,
	})
	err = userServiceAuthUnaryInterceptor(signer)(
		ctx,
		logicv1.AgentApprovalService_DecideApproval_FullMethodName,
		request,
		&logicv1.DecideApprovalResponse{},
		nil,
		func(ctx context.Context, method string, req, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			outgoing, ok := metadata.FromOutgoingContext(ctx)
			require.True(t, ok)
			incoming := metadata.NewIncomingContext(context.Background(), outgoing.Copy())
			claims, verifyErr := verifier.VerifyIncoming(incoming, method, req.(*logicv1.DecideApprovalRequest))
			require.NoError(t, verifyErr)
			require.Equal(t, "tenant-a", claims.TenantID)
			require.Equal(t, "admin", claims.Actor)
			require.Equal(t, int64(9), claims.PrincipalVersion)
			return nil
		},
	)
	require.NoError(t, err)
}

func TestUserServicesDoNotUseTransparentGRPCRetryWithSingleNonce(t *testing.T) {
	require.NotContains(t, serviceConfigJSON, "SessionService")
	require.NotContains(t, serviceConfigJSON, "ChatService")
	require.NotContains(t, serviceConfigJSON, "AgentApprovalService")
}
