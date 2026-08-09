package logicclient

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

func TestAgentIAMMutationClient_BusinessRetryReSignsWithFreshNonce(t *testing.T) {
	sequence := 0
	signer, err := serviceauth.NewSigner(
		"pilot-service",
		[]byte("0123456789abcdef0123456789abcdef"),
		serviceauth.WithNonceSource(func() (string, error) {
			sequence++
			return fmt.Sprintf("%032x", sequence), nil
		}),
	)
	require.NoError(t, err)
	client := &AgentIAMMutationClient{authenticator: signer, botUsername: "agent-bot"}
	request := &logicv1.GetExecutionApprovalRequest{
		TenantId: "tenant-a", CallId: "call-1",
		ArgsHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	first, err := client.authenticate(context.Background(), "tenant-a", logicv1.AgentIAMMutationService_GetExecutionApproval_FullMethodName, request)
	require.NoError(t, err)
	second, err := client.authenticate(context.Background(), "tenant-a", logicv1.AgentIAMMutationService_GetExecutionApproval_FullMethodName, request)
	require.NoError(t, err)
	firstMD, ok := metadata.FromOutgoingContext(first)
	require.True(t, ok)
	secondMD, ok := metadata.FromOutgoingContext(second)
	require.True(t, ok)
	require.NotEqual(t, firstMD.Get(serviceauth.HeaderNonce), secondMD.Get(serviceauth.HeaderNonce))
	require.NotEqual(t, firstMD.Get(serviceauth.HeaderSignature), secondMD.Get(serviceauth.HeaderSignature))
	require.Equal(t, 2, sequence)
}
