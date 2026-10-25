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

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

func TestUnsignedUsernameCannotImpersonatePilot(t *testing.T) {
	s := &GRPCServer{}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-username", "resonance-agent"))
	_, err := s.authUnaryInterceptor(ctx, &logicv1.SendEventRequest{}, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName}, func(context.Context, any) (any, error) { t.Fatal("untrusted request reached service"); return nil, nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
func TestPilotServiceSignatureBindsTenantAndBot(t *testing.T) {
	secret := []byte("pilot-service-secret-01234567890123456789")
	signer, err := serviceauth.NewSigner("pilot", secret)
	require.NoError(t, err)
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{MaxSkew: 30 * time.Second, Services: map[string]serviceauth.ServicePolicy{
		"pilot": {Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}}, AllowedMethods: map[string]struct{}{logicv1.ChatService_SendEvent_FullMethodName: {}}, AllowAnyTenant: true},
	}})
	require.NoError(t, err)
	request := &logicv1.SendEventRequest{SessionId: "private-chat"}
	hash, err := serviceauth.PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(context.Background(), "tenant-b", "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, hash)
	require.NoError(t, err)
	md, _ := metadata.FromOutgoingContext(outgoing)
	s := &GRPCServer{serviceAuth: verifier}
	_, err = s.authUnaryInterceptor(metadata.NewIncomingContext(context.Background(), md), request, &grpc.UnaryServerInfo{FullMethod: logicv1.ChatService_SendEvent_FullMethodName}, func(ctx context.Context, _ any) (any, error) {
		id, tenant, ok := service.ServicePrincipalFromCtx(ctx)
		require.True(t, ok)
		require.Equal(t, "pilot", id)
		require.Equal(t, "tenant-b", tenant)
		return &logicv1.SendEventResponse{}, nil
	})
	require.NoError(t, err)
}
