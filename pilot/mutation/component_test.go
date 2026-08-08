package mutation

import (
	"context"
	"testing"

	"github.com/ceyewan/genesis/mq"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
)

func TestComponent_ApprovalEventIsOnlySignalAndDuplicateDeliveryIsIdempotent(t *testing.T) {
	fixture := newMutationFixture(t)
	_, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), fixture.request())
	require.NoError(t, err)
	fixture.logic.approve("tenant-a", "call-1", "approver")
	payload, err := proto.Marshal(&mqv1.AgentApprovalDecidedEvent{
		TenantId: "tenant-a", CallId: "call-1", ArgsHash: "event-payload-is-not-authority",
		Decision:        mqv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT,
		ApprovalVersion: 999,
	})
	require.NoError(t, err)
	component := &Component{service: fixture.service}

	first := &fakeMQMessage{data: payload}
	require.NoError(t, component.handleMessage(first))
	require.Equal(t, 1, first.acks)
	require.Zero(t, first.naks)
	second := &fakeMQMessage{data: payload}
	require.NoError(t, component.handleMessage(second))
	require.Equal(t, 1, second.acks)
	require.Equal(t, 1, fixture.logic.commits)
}

type fakeMQMessage struct {
	data []byte
	acks int
	naks int
}

func (*fakeMQMessage) Context() context.Context { return context.Background() }
func (*fakeMQMessage) Topic() string            { return "resonance.agent.approval.decided.v1" }
func (m *fakeMQMessage) Data() []byte           { return m.data }
func (*fakeMQMessage) Headers() mq.Headers      { return nil }
func (m *fakeMQMessage) Ack() error             { m.acks++; return nil }
func (m *fakeMQMessage) Nak() error             { m.naks++; return nil }
func (*fakeMQMessage) ID() string               { return "message-1" }
