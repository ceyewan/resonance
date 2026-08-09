package grpctrace

import (
	"context"
	"testing"

	genesistrace "github.com/ceyewan/genesis/trace"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

func TestGatewayLogicMQTaskGatewayParentContinuity(t *testing.T) {
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

	tracer := provider.Tracer("phase-two-continuity")
	gatewayContext, gatewaySpan := tracer.Start(context.Background(), "gateway.ingress")

	logicContext := grpcRoundTrip(gatewayContext)
	logicContext, logicSpan := tracer.Start(logicContext, "logic.handle")

	mqCarrier := make(map[string]string, 2)
	genesistrace.Inject(logicContext, mqCarrier)
	taskContext := genesistrace.Extract(context.Background(), mqCarrier)
	taskContext, taskSpan := tracer.Start(taskContext, "task.consume")

	pushCarrier := make(map[string]string, 2)
	genesistrace.Inject(taskContext, pushCarrier)
	pusherContext := genesistrace.Extract(context.Background(), pushCarrier)
	finalGatewayContext := grpcRoundTrip(pusherContext)
	_, finalGatewaySpan := tracer.Start(finalGatewayContext, "gateway.push")

	finalGatewaySpan.End()
	taskSpan.End()
	logicSpan.End()
	gatewaySpan.End()

	spans := make(map[string]sdktrace.ReadOnlySpan)
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
	}
	require.Len(t, spans, 4)
	traceID := spans["gateway.ingress"].SpanContext().TraceID()
	for _, name := range []string{"logic.handle", "task.consume", "gateway.push"} {
		require.Equal(t, traceID, spans[name].SpanContext().TraceID(), name)
	}
	require.Equal(t, spans["gateway.ingress"].SpanContext().SpanID(), spans["logic.handle"].Parent().SpanID())
	require.Equal(t, spans["logic.handle"].SpanContext().SpanID(), spans["task.consume"].Parent().SpanID())
	require.Equal(t, spans["task.consume"].SpanContext().SpanID(), spans["gateway.push"].Parent().SpanID())
}

func TestInjectOutgoingReplacesStaleTraceMetadata(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	spanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: oteltrace.TraceID{1}, SpanID: oteltrace.SpanID{2}, TraceFlags: oteltrace.FlagsSampled,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("traceparent", "stale", "x-service", "preserved"))

	md, _ := metadata.FromOutgoingContext(InjectOutgoing(ctx))
	require.Len(t, md.Get("traceparent"), 1)
	require.NotEqual(t, "stale", md.Get("traceparent")[0])
	require.Equal(t, []string{"preserved"}, md.Get("x-service"))
}

func grpcRoundTrip(ctx context.Context) context.Context {
	outgoing := InjectOutgoing(ctx)
	md, _ := metadata.FromOutgoingContext(outgoing)
	incoming := metadata.NewIncomingContext(context.Background(), md)
	extracted := ExtractIncoming(incoming)
	spanContext := oteltrace.SpanContextFromContext(extracted)
	if spanContext.IsValid() && !spanContext.IsRemote() {
		spanContext = spanContext.WithRemote(true)
		extracted = oteltrace.ContextWithRemoteSpanContext(extracted, spanContext)
	}
	return extracted
}
