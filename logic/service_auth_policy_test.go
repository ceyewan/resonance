package logic

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

func TestServiceAuthMethodPoliciesSeparateGatewayUserPilotAndIAMPilot(t *testing.T) {
	userMethods := userPilotServiceMethods()
	_, ok := userMethods[logicv1.ChatService_SendEvent_FullMethodName]
	require.True(t, ok)
	_, ok = userMethods[logicv1.SessionService_GetHistoryEvents_FullMethodName]
	require.True(t, ok)
	_, ok = userMethods[logicv1.AgentApprovalService_CreateApproval_FullMethodName]
	require.False(t, ok)
	_, ok = userMethods[logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName]
	require.False(t, ok)

	iamMethods := iamPilotServiceMethods()
	_, ok = iamMethods[logicv1.AgentApprovalService_CreateApproval_FullMethodName]
	require.True(t, ok)
	_, ok = iamMethods[logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName]
	require.True(t, ok)

	gatewayMethods := gatewayServiceMethods()
	_, ok = gatewayMethods[logicv1.AgentApprovalService_DecideApproval_FullMethodName]
	require.True(t, ok)
	_, ok = gatewayMethods[logicv1.AgentApprovalService_CreateApproval_FullMethodName]
	require.False(t, ok)
	_, ok = gatewayMethods[logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName]
	require.False(t, ok)
}

func TestServiceAuthPoliciesRejectUserPilotPrivilegeAndTenantEscalation(t *testing.T) {
	userSecret := []byte("pilot-user-secret-0123456789abcdef")
	iamSecret := []byte("pilot-iam-secret-0123456789abcdef0")
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{
		MaxSkew: time.Minute,
		Services: map[string]serviceauth.ServicePolicy{
			"pilot-user-service": {
				Secret: userSecret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowedMethods: userPilotServiceMethods(), AllowedTenants: map[string]struct{}{"tenant-a": {}},
			},
			"pilot-iam-service": {
				Secret: iamSecret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowedMethods: iamPilotServiceMethods(), AllowedTenants: map[string]struct{}{"tenant-a": {}},
			},
		},
	})
	require.NoError(t, err)
	userSigner, err := serviceauth.NewSigner("pilot-user-service", userSecret)
	require.NoError(t, err)
	iamSigner, err := serviceauth.NewSigner("pilot-iam-service", iamSecret)
	require.NoError(t, err)

	mutation := &logicv1.ExecuteTenantMembershipStatusRequest{
		TenantId: "tenant-a", RunId: "run-1", CallId: "call-1",
	}
	require.ErrorIs(t, verifySignedCall(
		t, verifier, userSigner, "tenant-a",
		logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName, mutation,
	), serviceauth.ErrInvalidCredentials)
	require.NoError(t, verifySignedCall(
		t, verifier, iamSigner, "tenant-a",
		logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName, mutation,
	))

	chat := &logicv1.SendEventRequest{SessionId: "conversation-a"}
	require.ErrorIs(t, verifySignedCall(
		t, verifier, userSigner, "tenant-b", logicv1.ChatService_SendEvent_FullMethodName, chat,
	), serviceauth.ErrInvalidCredentials)
}

func verifySignedCall(
	t *testing.T,
	verifier *serviceauth.Verifier,
	signer *serviceauth.Signer,
	tenantID, method string,
	request proto.Message,
) error {
	t.Helper()
	payloadHash, err := serviceauth.PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(
		context.Background(), tenantID, "resonance-agent", method, payloadHash,
	)
	require.NoError(t, err)
	values, ok := metadata.FromOutgoingContext(outgoing)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), values.Copy())
	_, err = verifier.VerifyIncoming(incoming, method, request)
	return err
}
