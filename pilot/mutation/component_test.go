package mutation

import (
	"context"
	"testing"
	"time"

	"github.com/ceyewan/genesis/mq"
	genesistrace "github.com/ceyewan/genesis/trace"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/pkg/grpctrace"
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

func TestComponent_HumanDecisionMQPilotMutationLogicExecutionParentContinuity(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	fixture := newMutationFixture(t)
	_, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), fixture.request())
	require.NoError(t, err)
	fixture.logic.approve("tenant-a", "call-1", "approver")
	tracer := provider.Tracer("phase-two-agent-continuity")
	decisionContext, decisionSpan := tracer.Start(context.Background(), "human.approval.decision")
	carrier := make(map[string]string, 4)
	genesistrace.Inject(decisionContext, carrier)
	carrier["baggage"] = "approver=must-not-propagate"
	carrier["authorization"] = "must-not-propagate"
	fixture.logic.observeExecute = func(ctx context.Context) {
		outgoing := grpctrace.InjectOutgoing(ctx)
		outgoingMetadata, _ := metadata.FromOutgoingContext(outgoing)
		logicContext := grpctrace.ExtractIncoming(metadata.NewIncomingContext(context.Background(), outgoingMetadata))
		_, span := tracer.Start(logicContext, "logic.mutation.execute")
		span.End()
	}
	payload, err := proto.Marshal(&mqv1.AgentApprovalDecidedEvent{
		TenantId: "tenant-a", CallId: "call-1", TraceHeaders: carrier,
	})
	require.NoError(t, err)
	component := &Component{service: fixture.service}
	message := &fakeMQMessage{data: payload}
	require.NoError(t, component.handleMessage(message))
	decisionSpan.End()

	spans := make(map[string]sdktrace.ReadOnlySpan)
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
	}
	require.Contains(t, spans, "human.approval.decision")
	require.Contains(t, spans, "agent.mutation.consume")
	require.Contains(t, spans, "logic.mutation.execute")
	require.Equal(t, spans["human.approval.decision"].SpanContext().TraceID(), spans["agent.mutation.consume"].SpanContext().TraceID())
	require.Equal(t, spans["human.approval.decision"].SpanContext().SpanID(), spans["agent.mutation.consume"].Parent().SpanID())
	require.Equal(t, spans["agent.mutation.consume"].SpanContext().SpanID(), spans["logic.mutation.execute"].Parent().SpanID())
}

type fakeMQMessage struct {
	data []byte
	acks int
	naks int
}

func (*fakeMQMessage) Context() context.Context           { return context.Background() }
func (*fakeMQMessage) Topic() string                      { return "resonance.agent.approval.decided.v1" }
func (m *fakeMQMessage) Data() []byte                     { return m.data }
func (*fakeMQMessage) Headers() mq.Headers                { return nil }
func (m *fakeMQMessage) Ack() error                       { m.acks++; return nil }
func (m *fakeMQMessage) Nak() error                       { m.naks++; return nil }
func (m *fakeMQMessage) NakWithDelay(time.Duration) error { return m.Nak() }
func (*fakeMQMessage) ID() string                         { return "message-1" }
