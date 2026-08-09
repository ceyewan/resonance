package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/metrics"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestTelemetry_CloseAlwaysAttemptsTraceAndMeterAndIsIdempotent(t *testing.T) {
	meter := newTestMeter()
	traceCalls := 0
	telemetry, err := newWithMeter(meter, func(context.Context) error {
		traceCalls++
		return errors.New("trace close")
	})
	require.NoError(t, err)

	err = telemetry.Close(context.Background())
	require.ErrorContains(t, err, "trace close")
	require.Equal(t, 1, traceCalls)
	require.Equal(t, 1, meter.shutdownCalls)

	err = telemetry.Close(context.Background())
	require.ErrorContains(t, err, "trace close")
	require.Equal(t, 1, traceCalls)
	require.Equal(t, 1, meter.shutdownCalls)
}

func TestTelemetryExposesServiceMeterForGenesisComponents(t *testing.T) {
	meter := newTestMeter()
	telemetry, err := newWithMeter(meter, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Same(t, meter, telemetry.Meter())

	_, err = connector.NewRedis(
		&connector.RedisConfig{Addr: "127.0.0.1:6379"},
		connector.WithMeter(telemetry.Meter()),
	)
	require.NoError(t, err)
	require.Contains(t, meter.counters, connector.MetricHealthChecks)
	require.Contains(t, meter.counters, connector.MetricReconnects)
}

func TestObservedEventSink_RecordsBoundedRunSignalsWithoutIdentityLabels(t *testing.T) {
	meter := newTestMeter()
	telemetry, err := newWithMeter(meter, func(context.Context) error { return nil })
	require.NoError(t, err)
	delegate := &testRuntimeSink{err: errors.New("stream full")}
	sink, err := telemetry.ObserveRuntimeEvents(delegate)
	require.NoError(t, err)

	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	now := base
	sink.now = func() time.Time { return now }
	run := &model.AgentRun{RunID: "secret-run-id", TenantID: "secret-tenant", ActorUsername: "secret-user", QueuedAt: base.Add(-2 * time.Second)}

	require.Error(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))
	now = base.Add(500 * time.Millisecond)
	require.Error(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventTextDelta, Text: "hello"}))
	now = base.Add(time.Second)
	require.Error(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{
		Kind: pilotruntime.EventToolEnded, Tool: &pilotruntime.ToolEvent{CallID: "secret-call", Name: "unknown-dynamic-name", IsError: true},
	}))
	now = base.Add(3 * time.Second)
	require.Error(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{
		Kind:  pilotruntime.EventSettled,
		Usage: &pilotruntime.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 2, Cost: 0.25},
	}))

	require.Len(t, sink.active, 0)
	require.Equal(t, []float64{2}, meter.histograms["pilot_run_queue_wait_seconds"].values)
	require.Equal(t, []float64{0.5}, meter.histograms["pilot_first_token_seconds"].values)
	require.Equal(t, []float64{3}, meter.histograms["pilot_run_duration_seconds"].values)
	require.Equal(t, float64(0), meter.gauges["pilot_active_runs"].value)
	require.Equal(t, float64(1), meter.counters["pilot_tool_calls_total"].value)
	require.Equal(t, float64(16), meter.counters["pilot_model_tokens_total"].value)
	require.Equal(t, 0.25, meter.counters["pilot_model_cost_total"].value)
	require.Equal(t, float64(4), meter.counters["pilot_stream_publish_dropped_total"].value)

	for _, instrumentLabels := range meter.allLabels() {
		for _, label := range instrumentLabels {
			require.NotContains(t, []string{"tenant_id", "run_id", "username", "conversation_id", "call_id", "tool_name"}, label.Key)
			require.NotContains(t, label.Value, "secret")
		}
	}
}

func TestExtractPersistedTraceContext_RestoresOnlyBoundedW3CHeaders(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	valid := []byte(`{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","baggage":"tenant_id=secret","authorization":"secret"}`)
	ctx := ExtractPersistedTraceContext(context.Background(), valid)
	span := trace.SpanContextFromContext(ctx)
	require.True(t, span.IsValid())
	require.True(t, span.IsRemote())
	require.Zero(t, baggage.FromContext(ctx).Len())

	attackerContext := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{9}, SpanID: trace.SpanID{8}, TraceFlags: trace.FlagsSampled,
	}))
	malformed := ExtractPersistedTraceContext(attackerContext, []byte(`{"traceparent":"bad\r\nheader"}`))
	require.False(t, trace.SpanContextFromContext(malformed).IsValid())
	empty := ExtractPersistedTraceContext(attackerContext, nil)
	require.False(t, trace.SpanContextFromContext(empty).IsValid())
}

type testRuntimeSink struct{ err error }

func (s *testRuntimeSink) PublishRuntimeEvent(context.Context, *model.AgentRun, pilotruntime.RuntimeEvent) error {
	return s.err
}

type testMeter struct {
	counters      map[string]*testCounter
	gauges        map[string]*testGauge
	histograms    map[string]*testHistogram
	shutdownCalls int
}

func newTestMeter() *testMeter {
	return &testMeter{
		counters: make(map[string]*testCounter), gauges: make(map[string]*testGauge),
		histograms: make(map[string]*testHistogram),
	}
}

func (m *testMeter) Counter(name, _ string, _ ...metrics.MetricOption) (metrics.Counter, error) {
	counter := &testCounter{}
	m.counters[name] = counter
	return counter, nil
}

func (m *testMeter) Gauge(name, _ string, _ ...metrics.MetricOption) (metrics.Gauge, error) {
	gauge := &testGauge{}
	m.gauges[name] = gauge
	return gauge, nil
}

func (m *testMeter) Histogram(name, _ string, _ ...metrics.MetricOption) (metrics.Histogram, error) {
	histogram := &testHistogram{}
	m.histograms[name] = histogram
	return histogram, nil
}

func (m *testMeter) Shutdown(context.Context) error {
	m.shutdownCalls++
	return nil
}

func (m *testMeter) allLabels() [][]metrics.Label {
	result := make([][]metrics.Label, 0)
	for _, counter := range m.counters {
		result = append(result, counter.labels...)
	}
	for _, gauge := range m.gauges {
		result = append(result, gauge.labels...)
	}
	for _, histogram := range m.histograms {
		result = append(result, histogram.labels...)
	}
	return result
}

type testCounter struct {
	value  float64
	labels [][]metrics.Label
}

func (c *testCounter) Inc(_ context.Context, labels ...metrics.Label) {
	c.Add(context.Background(), 1, labels...)
}
func (c *testCounter) Add(_ context.Context, value float64, labels ...metrics.Label) {
	c.value += value
	c.labels = append(c.labels, append([]metrics.Label(nil), labels...))
}

type testGauge struct {
	value  float64
	labels [][]metrics.Label
}

func (g *testGauge) Set(_ context.Context, value float64, labels ...metrics.Label) {
	g.value = value
	g.labels = append(g.labels, append([]metrics.Label(nil), labels...))
}
func (g *testGauge) Inc(_ context.Context, labels ...metrics.Label) {
	g.Set(context.Background(), g.value+1, labels...)
}
func (g *testGauge) Dec(_ context.Context, labels ...metrics.Label) {
	g.Set(context.Background(), g.value-1, labels...)
}

type testHistogram struct {
	values []float64
	labels [][]metrics.Label
}

func (h *testHistogram) Record(_ context.Context, value float64, labels ...metrics.Label) {
	h.values = append(h.values, value)
	h.labels = append(h.labels, append([]metrics.Label(nil), labels...))
}
