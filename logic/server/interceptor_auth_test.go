package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

func TestAuthInterceptor_AcceptsVerifiedServicePrincipal(t *testing.T) {
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	signer, verifier := testServiceAuthPair(t, now, secret)
	request := authTestRequest()
	payloadHash, err := serviceauth.PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(
		context.Background(), "tenant-a", "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, payloadHash,
	)
	require.NoError(t, err)
	values, ok := metadata.FromOutgoingContext(outgoing)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), values)

	server := &GRPCServer{serviceAuth: verifier, serviceProfiles: map[string]service.ServiceProfile{
		"pilot-service": {ProfileID: "user-assistant", ProfileVersion: 3},
	}}
	_, err = server.authUnaryInterceptor(incoming, request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			username, usernameErr := service.MustUsernameFromCtx(ctx)
			require.NoError(t, usernameErr)
			require.Equal(t, "resonance-agent", username)
			serviceID, tenantID, principalOK := service.ServicePrincipalFromCtx(ctx)
			require.True(t, principalOK)
			require.Equal(t, "pilot-service", serviceID)
			require.Equal(t, "tenant-a", tenantID)
			profile, profileOK := service.ServiceProfileFromCtx(ctx)
			require.True(t, profileOK)
			require.Equal(t, "user-assistant", profile.ProfileID)
			require.Equal(t, int64(3), profile.ProfileVersion)
			return &logicv1.SendEventResponse{}, nil
		})
	require.NoError(t, err)
}

func TestAuthInterceptor_InvalidServiceCredentialCannotDowngradeToUsernameMetadata(t *testing.T) {
	request := authTestRequest()
	incoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		serviceauth.HeaderServiceID, "pilot-service",
		serviceauth.HeaderSignature, "invalid",
		usernameMetadataKey, "resonance-agent",
	))
	server := &GRPCServer{}
	called := false
	_, err := server.authUnaryInterceptor(incoming, request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
		func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestAuthInterceptor_ProtectedBotCannotUseLegacyUsernameMetadata(t *testing.T) {
	request := authTestRequest()
	incoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs(usernameMetadataKey, "resonance-agent"))
	server := &GRPCServer{protectedActors: map[string]struct{}{"resonance-agent": {}}}
	called := false
	_, err := server.authUnaryInterceptor(incoming, request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
		func(context.Context, any) (any, error) { called = true; return nil, nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestAuthInterceptor_RejectsAmbiguousLegacyUsernameMetadata(t *testing.T) {
	request := authTestRequest()
	incoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		usernameMetadataKey, "alice", usernameMetadataKey, "bob",
	))
	server := &GRPCServer{}
	called := false
	_, err := server.authUnaryInterceptor(incoming, request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
		func(context.Context, any) (any, error) { called = true; return nil, nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestAuthInterceptor_LegacyUsernameIsTestOnly(t *testing.T) {
	request := authTestRequest()
	incoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs(usernameMetadataKey, "alice"))
	called := false
	server := &GRPCServer{}
	_, err := server.authUnaryInterceptor(incoming, request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
		func(context.Context, any) (any, error) { called = true; return nil, nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)

	server.allowLegacyUsernameAuth = true
	_, err = server.authUnaryInterceptor(incoming, request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			called = true
			_, verified := service.UserPrincipalFromCtx(ctx)
			require.False(t, verified, "legacy compatibility cannot create a trusted user principal")
			return &logicv1.SendEventResponse{}, nil
		})
	require.NoError(t, err)
	require.True(t, called)
}

func TestAuthInterceptor_ApprovalRejectsUntrustedTenantMetadata(t *testing.T) {
	request := &logicv1.GetApprovalRequest{TenantId: "tenant-a", CallId: "call-1"}
	server := &GRPCServer{}
	untrustedTenant := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		usernameMetadataKey, "alice",
		"x-tenant-id", "tenant-a",
	))
	called := false
	_, err := server.authUnaryInterceptor(
		untrustedTenant,
		request,
		&grpc.UnaryServerInfo{FullMethod: logicv1.AgentApprovalService_GetApproval_FullMethodName},
		func(context.Context, any) (any, error) { called = true; return nil, nil },
	)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestAuthInterceptor_GatewaySignatureBuildsAuthoritativeUserPrincipal(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	secret := []byte("gateway-secret-0123456789abcdef01")
	signer, verifier := testGatewayServiceAuthPair(t, now, secret)
	request := &logicv1.GetApprovalRequest{TenantId: "tenant-a", CallId: "call-1"}
	hash, err := serviceauth.PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateUserCall(
		context.Background(), "tenant-a", "alice", 7,
		logicv1.AgentApprovalService_GetApproval_FullMethodName, hash,
	)
	require.NoError(t, err)
	outgoingMD, ok := metadata.FromOutgoingContext(outgoing)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), outgoingMD.Copy())
	server := &GRPCServer{
		serviceAuth:      verifier,
		gatewayServiceID: "gateway-service",
		userPrincipalResolver: principalResolverFunc(func(_ context.Context, tenantID, username string) (*service.UserPrincipal, error) {
			require.Equal(t, "tenant-a", tenantID)
			require.Equal(t, "alice", username)
			return &service.UserPrincipal{
				TenantID: "tenant-a", Username: "alice", Version: 7,
				Roles: []string{"user"}, Scopes: []string{"chat:use"},
			}, nil
		}),
	}
	_, err = server.authUnaryInterceptor(
		incoming,
		request,
		&grpc.UnaryServerInfo{FullMethod: logicv1.AgentApprovalService_GetApproval_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			principal, ok := service.UserPrincipalFromCtx(ctx)
			require.True(t, ok)
			require.Equal(t, "alice", principal.Username)
			require.Equal(t, "tenant-a", principal.TenantID)
			require.Equal(t, []string{"user"}, principal.Roles)
			require.Equal(t, []string{"chat:use"}, principal.Scopes)
			require.Equal(t, int64(7), principal.Version)
			_, _, servicePrincipal := service.ServicePrincipalFromCtx(ctx)
			require.False(t, servicePrincipal)
			return &logicv1.GetApprovalResponse{}, nil
		},
	)
	require.NoError(t, err)
}

func TestAuthInterceptor_GatewayPrincipalFailsClosedOnVersionOrRepositoryFailure(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 30, 0, 0, time.UTC)
	secret := []byte("gateway-secret-0123456789abcdef01")
	request := authTestRequest()
	tests := map[string]principalResolverFunc{
		"stale version": func(context.Context, string, string) (*service.UserPrincipal, error) {
			return &service.UserPrincipal{TenantID: "tenant-a", Username: "alice", Version: 8}, nil
		},
		"disabled or missing membership": func(context.Context, string, string) (*service.UserPrincipal, error) {
			return nil, context.Canceled
		},
		"cross tenant result": func(context.Context, string, string) (*service.UserPrincipal, error) {
			return &service.UserPrincipal{TenantID: "tenant-b", Username: "alice", Version: 7}, nil
		},
	}
	for name, resolver := range tests {
		t.Run(name, func(t *testing.T) {
			signer, verifier := testGatewayServiceAuthPair(t, now, secret)
			hash, err := serviceauth.PayloadHash(request)
			require.NoError(t, err)
			outgoing, err := signer.AuthenticateUserCall(
				context.Background(), "tenant-a", "alice", 7,
				logicv1.ChatService_SendEvent_FullMethodName, hash,
			)
			require.NoError(t, err)
			outgoingMD, ok := metadata.FromOutgoingContext(outgoing)
			require.True(t, ok)
			incoming := metadata.NewIncomingContext(context.Background(), outgoingMD.Copy())
			server := &GRPCServer{
				serviceAuth: verifier, gatewayServiceID: "gateway-service", userPrincipalResolver: resolver,
			}
			called := false
			_, err = server.authUnaryInterceptor(
				incoming, request,
				&grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
				func(context.Context, any) (any, error) { called = true; return nil, nil },
			)
			require.Equal(t, codes.Unauthenticated, status.Code(err))
			require.False(t, called)
		})
	}
}

func TestAuthInterceptor_BearerOrPlaintextPrincipalCannotForgePrincipal(t *testing.T) {
	request := authTestRequest()
	server := &GRPCServer{}
	tests := []metadata.MD{
		metadata.Pairs("authorization", "Bearer stolen-user-token"),
		metadata.Pairs(usernameMetadataKey, "alice", "x-roles", "iam-admin"),
		metadata.Pairs(usernameMetadataKey, "alice", "x-scopes", "iam:users:write"),
	}
	for _, headers := range tests {
		called := false
		_, err := server.authUnaryInterceptor(
			metadata.NewIncomingContext(context.Background(), headers), request,
			&grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName},
			func(context.Context, any) (any, error) { called = true; return nil, nil },
		)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		require.False(t, called)
	}
}

type principalResolverFunc func(context.Context, string, string) (*service.UserPrincipal, error)

func (f principalResolverFunc) ResolveUserPrincipal(ctx context.Context, tenantID, username string) (*service.UserPrincipal, error) {
	return f(ctx, tenantID, username)
}

func testServiceAuthPair(t *testing.T, now time.Time, secret []byte) (*serviceauth.Signer, *serviceauth.Verifier) {
	t.Helper()
	signer, err := serviceauth.NewSigner("pilot-service", secret,
		serviceauth.WithSignerClock(func() time.Time { return now }),
		serviceauth.WithNonceSource(func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }),
	)
	require.NoError(t, err)
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]serviceauth.ServicePolicy{
			"pilot-service": {
				Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowAnyMethod: true, AllowAnyTenant: true,
			},
		},
	}, serviceauth.WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)
	return signer, verifier
}

func testGatewayServiceAuthPair(t *testing.T, now time.Time, secret []byte) (*serviceauth.Signer, *serviceauth.Verifier) {
	t.Helper()
	signer, err := serviceauth.NewSigner("gateway-service", secret,
		serviceauth.WithSignerClock(func() time.Time { return now }),
		serviceauth.WithNonceSource(func() (string, error) { return "66666666666666666666666666666666", nil }),
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
	return signer, verifier
}

func authTestRequest() *logicv1.SendEventRequest {
	return &logicv1.SendEventRequest{
		SessionId: "conversation-a",
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "answer", ClientMsgId: "agent:run:final",
		}},
	}
}
