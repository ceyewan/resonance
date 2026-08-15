// Package observability owns Pilot trace and metrics resources.
package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	genesistrace "github.com/ceyewan/genesis/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

const (
	serviceName = "resonance-pilot"
	tracerName  = "pilot-service"
)

type Telemetry struct {
	meter         metrics.Meter
	traceShutdown func(context.Context) error

	runQueueWait   metrics.Histogram
	firstToken     metrics.Histogram
	runDuration    metrics.Histogram
	activeRuns     metrics.Gauge
	runtimeEvents  metrics.Counter
	runtimeFailure metrics.Counter
	toolCalls      metrics.Counter
	tokens         metrics.Counter
	cost           metrics.Counter
	streamDropped  metrics.Counter

	closeOnce sync.Once
	closeErr  error
}

func New(config Config) (*Telemetry, error) {
	applyResourceDefaults(&config)
	traceShutdown, err := initTrace(config)
	if err != nil {
		return nil, fmt.Errorf("pilot trace: %w", err)
	}
	meter, err := metrics.New(&metrics.Config{
		ServiceName: serviceName, Version: config.Version, InstanceID: config.InstanceID, Environment: config.Environment, Port: config.Metrics.Port,
		Path: config.Metrics.Path, EnableRuntime: config.Metrics.EnableRuntime,
	})
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(fmt.Errorf("pilot metrics: %w", err), traceShutdown(shutdownContext))
	}
	telemetry, err := newWithMeter(meter, traceShutdown)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, meter.Shutdown(shutdownContext), traceShutdown(shutdownContext))
	}
	return telemetry, nil
}

func newWithMeter(meter metrics.Meter, traceShutdown func(context.Context) error) (*Telemetry, error) {
	if meter == nil || traceShutdown == nil {
		return nil, fmt.Errorf("pilot telemetry dependencies are incomplete")
	}
	telemetry := &Telemetry{meter: meter, traceShutdown: traceShutdown}
	var err error
	if telemetry.runQueueWait, err = meter.Histogram(
		"pilot_run_queue_wait_seconds", "Time an agent run waits before runtime start",
		metrics.WithUnit("s"), metrics.WithBuckets([]float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}),
	); err != nil {
		return nil, err
	}
	if telemetry.firstToken, err = meter.Histogram(
		"pilot_first_token_seconds", "Time from runtime start to first text delta",
		metrics.WithUnit("s"), metrics.WithBuckets([]float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}),
	); err != nil {
		return nil, err
	}
	if telemetry.runDuration, err = meter.Histogram(
		"pilot_run_duration_seconds", "Agent runtime duration by terminal outcome",
		metrics.WithUnit("s"), metrics.WithBuckets([]float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}),
	); err != nil {
		return nil, err
	}
	if telemetry.activeRuns, err = meter.Gauge("pilot_active_runs", "Current active agent runtimes"); err != nil {
		return nil, err
	}
	if telemetry.runtimeEvents, err = meter.Counter("pilot_runtime_events_total", "Runtime-neutral events observed by kind"); err != nil {
		return nil, err
	}
	if telemetry.runtimeFailure, err = meter.Counter("pilot_runtime_failures_total", "Agent runtime terminal failures"); err != nil {
		return nil, err
	}
	if telemetry.toolCalls, err = meter.Counter("pilot_tool_calls_total", "Tool executions by terminal outcome"); err != nil {
		return nil, err
	}
	if telemetry.tokens, err = meter.Counter("pilot_model_tokens_total", "Model tokens by bounded category"); err != nil {
		return nil, err
	}
	if telemetry.cost, err = meter.Counter("pilot_model_cost_total", "Model cost reported by the provider"); err != nil {
		return nil, err
	}
	if telemetry.streamDropped, err = meter.Counter("pilot_stream_publish_dropped_total", "Best-effort stream events dropped by bounded egress"); err != nil {
		return nil, err
	}
	return telemetry, nil
}

// Meter returns the service-owned Meter shared with Genesis components.
func (t *Telemetry) Meter() metrics.Meter { return t.meter }

func initTrace(config Config) (func(context.Context) error, error) {
	if config.Trace.Disable {
		return genesistrace.InstallLocalProvider(serviceName)
	}
	return genesistrace.Init(&genesistrace.Config{ServiceName: serviceName, Version: config.Version,
		InstanceID: config.InstanceID, Environment: config.Environment, Endpoint: config.Trace.Endpoint,
		Sampler: config.Trace.Sampler, Batcher: genesistrace.BatcherBatch, Insecure: config.Trace.Insecure})
}

func applyResourceDefaults(config *Config) {
	if config.Version == "" {
		config.Version = "dev"
	}
	if config.InstanceID == "" {
		config.InstanceID, _ = os.Hostname()
	}
	if config.Environment == "" {
		config.Environment = "development"
	}
	if config.Trace.Endpoint == "" {
		config.Trace.Endpoint = "localhost:4317"
	}
	if config.Trace.Sampler == 0 {
		config.Trace.Sampler = 1
	}
}

func (t *Telemetry) Close(ctx context.Context) error {
	t.closeOnce.Do(func() {
		// Both resources are always attempted. A trace failure must not leak the
		// Prometheus listener or meter provider.
		t.closeErr = errors.Join(t.traceShutdown(ctx), t.meter.Shutdown(ctx))
	})
	return t.closeErr
}

// RecordShutdownFlushProbe creates a service-specific span immediately before
// the trace provider is flushed. Its context-bound log is the recovery proof.
func RecordShutdownFlushProbe(logger clog.Logger) {
	if logger == nil {
		return
	}
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "stage3.shutdown.flush")
	logger.InfoContext(ctx, "stage3 shutdown trace flush probe", clog.String("service", serviceName))
	span.End()
}

func (t *Telemetry) recordUsage(ctx context.Context, usage *pilotruntime.Usage) {
	if usage == nil {
		return
	}
	values := []struct {
		category string
		value    int64
	}{
		{"input", usage.InputTokens},
		{"output", usage.OutputTokens},
		{"cache_read", usage.CacheReadTokens},
		{"cache_write", usage.CacheWriteTokens},
	}
	for _, value := range values {
		if value.value > 0 {
			t.tokens.Add(ctx, float64(value.value), metrics.L("category", value.category))
		}
	}
	if usage.Cost > 0 {
		t.cost.Add(ctx, usage.Cost)
	}
}

// ExtractPersistedTraceContext restores only W3C trace headers from the
// durable AgentRun carrier. User-controlled arbitrary headers are ignored.
func ExtractPersistedTraceContext(ctx context.Context, encoded []byte) context.Context {
	if len(encoded) == 0 || len(encoded) > 16*1024 {
		return clearUntrustedTraceContext(ctx)
	}
	var source map[string]string
	if err := json.Unmarshal(encoded, &source); err != nil {
		return clearUntrustedTraceContext(ctx)
	}
	return ExtractTraceContext(ctx, source)
}

// ExtractTraceContext treats source as a durable, service-authored carrier. It
// replaces any transport context and restores only bounded W3C trace headers.
func ExtractTraceContext(ctx context.Context, source map[string]string) context.Context {
	carrier := map[string]string{}
	for _, key := range []string{"traceparent", "tracestate"} {
		value := strings.TrimSpace(source[key])
		if value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00") {
			carrier[key] = value
		}
	}
	return genesistrace.Extract(clearUntrustedTraceContext(ctx), carrier)
}

func clearUntrustedTraceContext(ctx context.Context) context.Context {
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	return baggage.ContextWithBaggage(ctx, baggage.Baggage{})
}

func StartSpan(ctx context.Context, name string) (context.Context, func()) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, name)
	return ctx, func() { span.End() }
}

type RuntimeEventSink interface {
	PublishRuntimeEvent(context.Context, *model.AgentRun, pilotruntime.RuntimeEvent) error
}

type ObservedEventSink struct {
	telemetry *Telemetry
	delegate  RuntimeEventSink
	now       func() time.Time

	mu     sync.Mutex
	active map[string]*runObservation
}

type runObservation struct {
	startedAt  time.Time
	firstToken bool
}

func (t *Telemetry) ObserveRuntimeEvents(delegate RuntimeEventSink) (*ObservedEventSink, error) {
	if t == nil || delegate == nil {
		return nil, fmt.Errorf("observed runtime event sink dependencies are incomplete")
	}
	return &ObservedEventSink{telemetry: t, delegate: delegate, now: time.Now, active: make(map[string]*runObservation)}, nil
}

func (s *ObservedEventSink) PublishRuntimeEvent(
	ctx context.Context,
	run *model.AgentRun,
	event pilotruntime.RuntimeEvent,
) error {
	if run != nil && run.RunID != "" {
		s.observe(ctx, run, event)
	}
	err := s.delegate.PublishRuntimeEvent(ctx, run, event)
	if err != nil {
		s.telemetry.streamDropped.Inc(ctx)
	}
	return err
}

func (s *ObservedEventSink) observe(ctx context.Context, run *model.AgentRun, event pilotruntime.RuntimeEvent) {
	now := s.now()
	s.telemetry.runtimeEvents.Inc(ctx, metrics.L("kind", boundedEventKind(event.Kind)))

	s.mu.Lock()
	observation := s.active[run.RunID]
	switch event.Kind {
	case pilotruntime.EventStarted:
		if observation == nil {
			observation = &runObservation{startedAt: now}
			s.active[run.RunID] = observation
			s.telemetry.activeRuns.Inc(ctx)
			if !run.QueuedAt.IsZero() && now.After(run.QueuedAt) {
				s.telemetry.runQueueWait.Record(ctx, now.Sub(run.QueuedAt).Seconds())
			}
		}
	case pilotruntime.EventTextDelta:
		if observation != nil && !observation.firstToken {
			observation.firstToken = true
			s.telemetry.firstToken.Record(ctx, now.Sub(observation.startedAt).Seconds())
		}
	case pilotruntime.EventToolEnded:
		outcome := "success"
		if event.Tool == nil || event.Tool.IsError {
			outcome = "error"
		}
		s.telemetry.toolCalls.Inc(ctx, metrics.L("outcome", outcome))
	case pilotruntime.EventSettled, pilotruntime.EventFailed:
		if event.Kind == pilotruntime.EventFailed {
			s.telemetry.runtimeFailure.Inc(ctx)
		}
		if observation != nil {
			outcome := "settled"
			if event.Kind == pilotruntime.EventFailed {
				outcome = "failed"
			}
			s.telemetry.runDuration.Record(ctx, now.Sub(observation.startedAt).Seconds(), metrics.L("outcome", outcome))
			s.telemetry.activeRuns.Dec(ctx)
			delete(s.active, run.RunID)
		}
		if event.Kind == pilotruntime.EventSettled {
			s.telemetry.recordUsage(ctx, event.Usage)
		}
	}
	s.mu.Unlock()
}

func boundedEventKind(kind pilotruntime.EventKind) string {
	switch kind {
	case pilotruntime.EventStarted, pilotruntime.EventTextDelta,
		pilotruntime.EventToolStarted, pilotruntime.EventToolUpdated, pilotruntime.EventToolEnded,
		pilotruntime.EventCompactionStarted, pilotruntime.EventCompactionEnded,
		pilotruntime.EventRetryStarted, pilotruntime.EventRetryEnded,
		pilotruntime.EventSettled, pilotruntime.EventFailed:
		return string(kind)
	default:
		return "unknown"
	}
}
