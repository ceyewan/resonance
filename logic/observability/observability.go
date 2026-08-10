// Package observability 提供 Logic 服务的可观测性支持
// 包括 Trace（分布式追踪）和 Metrics（指标收集）
package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	genesistrace "github.com/ceyewan/genesis/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const (
	// ServiceName 服务名称
	ServiceName = "resonance-logic"

	// TracerName Tracer 名称
	TracerName = "logic-service"
)

var (
	// 全局组件
	meter    metrics.Meter
	initOnce sync.Once
	initErr  error
	shutdown func(context.Context) error

	// 业务指标
	loginDuration                metrics.Histogram
	registerDuration             metrics.Histogram
	sendMessageDuration          metrics.Histogram
	createSessionDuration        metrics.Histogram
	defaultAgentSessionProvision metrics.Counter
	outboxBacklog                metrics.Gauge
)

// Init 初始化可观测性组件
func Init(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("observability config is required")
	}
	applyResourceDefaults(cfg)
	initOnce.Do(func() {
		// 1. 初始化 Trace
		shutdownFunc, err := initTrace(cfg)
		if err != nil {
			initErr = fmt.Errorf("init trace: %w", err)
			return
		}
		shutdown = shutdownFunc

		// 2. 初始化 Metrics
		meter, err = initMetrics(cfg)
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			initErr = errors.Join(fmt.Errorf("init metrics: %w", err), shutdown(cleanupCtx))
			cancel()
			return
		}

		// 3. 初始化业务指标
		initBusinessMetrics()
	})

	return initErr
}

// Meter returns the service-owned Meter shared with Genesis components.
func Meter() metrics.Meter { return meter }

// Shutdown 优雅关闭
func Shutdown(ctx context.Context) error {
	var traceErr, metricsErr error
	if shutdown != nil {
		traceErr = shutdown(ctx)
	}
	if meter != nil {
		metricsErr = meter.Shutdown(ctx)
	}
	return errors.Join(traceErr, metricsErr)
}

// RecordShutdownFlushProbe creates a service-specific span immediately before
// the trace provider is flushed. Its context-bound log is the recovery proof.
func RecordShutdownFlushProbe(logger clog.Logger) {
	if logger == nil {
		return
	}
	ctx, span := otel.Tracer(TracerName).Start(context.Background(), "stage3.shutdown.flush")
	logger.InfoContext(ctx, "stage3 shutdown trace flush probe", clog.String("service", ServiceName))
	span.End()
}

// initTrace 初始化 Trace
func initTrace(cfg *Config) (func(context.Context) error, error) {
	if cfg.Trace.Disable {
		return genesistrace.InstallLocalProvider(ServiceName)
	}
	return genesistrace.Init(&genesistrace.Config{ServiceName: ServiceName, Version: cfg.Version, InstanceID: cfg.InstanceID,
		Environment: cfg.Environment, Endpoint: cfg.Trace.Endpoint, Sampler: cfg.Trace.Sampler,
		Batcher: genesistrace.BatcherBatch, Insecure: cfg.Trace.Insecure})
}

func applyResourceDefaults(cfg *Config) {
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID, _ = os.Hostname()
	}
	if cfg.Environment == "" {
		cfg.Environment = "development"
	}
	if cfg.Trace.Endpoint == "" {
		cfg.Trace.Endpoint = "localhost:4317"
	}
	if cfg.Trace.Sampler == 0 {
		cfg.Trace.Sampler = 1
	}
}

// initMetrics 初始化 Metrics
func initMetrics(cfg *Config) (metrics.Meter, error) {
	metricsCfg := &metrics.Config{
		ServiceName: ServiceName,
		Version:     cfg.Version, InstanceID: cfg.InstanceID, Environment: cfg.Environment,
		Port:          cfg.Metrics.Port,
		Path:          cfg.Metrics.Path,
		EnableRuntime: cfg.Metrics.EnableRuntime,
	}
	if metricsCfg.Port == 0 {
		metricsCfg.Port = 9091
	}
	if metricsCfg.Path == "" {
		metricsCfg.Path = "/metrics"
	}

	return metrics.New(metricsCfg)
}

// initBusinessMetrics 初始化业务指标
func initBusinessMetrics() {
	// Login 处理耗时
	loginDuration, _ = meter.Histogram(
		"logic_login_duration_seconds",
		"Login request processing duration",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}),
	)

	// Register 处理耗时
	registerDuration, _ = meter.Histogram(
		"logic_register_duration_seconds",
		"Register request processing duration",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}),
	)

	// SendMessage 处理耗时
	sendMessageDuration, _ = meter.Histogram(
		"logic_send_message_duration_seconds",
		"SendMessage request processing duration",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5}),
	)

	// CreateSession 处理耗时
	createSessionDuration, _ = meter.Histogram(
		"logic_create_session_duration_seconds",
		"CreateSession request processing duration",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}),
	)

	defaultAgentSessionProvision, _ = meter.Counter(
		"logic_default_agent_session_provision_total",
		"Default Agent session provisioning attempts by bounded trigger and outcome",
	)
	outboxBacklog, _ = meter.Gauge(
		"logic_outbox_backlog",
		"Current pending outbox batch size observed by the relay",
	)
}

// ============================================================================
// Trace 辅助函数
// ============================================================================

// StartSpan 开始一个新的 Span
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func()) {
	tracer := otel.Tracer(TracerName)
	ctx, span := tracer.Start(ctx, name)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, func() {
		span.End()
	}
}

// ExtractTraceContext 从 map 中提取 Trace Context
func ExtractTraceContext(ctx context.Context, traceHeaders map[string]string) context.Context {
	if len(traceHeaders) == 0 {
		return ctx
	}
	return genesistrace.Extract(ctx, traceHeaders)
}

// InjectTraceContext 将当前 Context 的 Trace 信息注入到 map
func InjectTraceContext(ctx context.Context, carrier map[string]string) {
	if carrier == nil {
		return
	}
	genesistrace.Inject(ctx, carrier)
}

// ============================================================================
// Metrics 记录函数
// ============================================================================

// RecordLoginDuration 记录 Login 处理耗时
func RecordLoginDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if loginDuration != nil {
		loginDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordRegisterDuration 记录 Register 处理耗时
func RecordRegisterDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if registerDuration != nil {
		registerDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordSendMessageDuration 记录 SendMessage 处理耗时
func RecordSendMessageDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if sendMessageDuration != nil {
		sendMessageDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordCreateSessionDuration 记录 CreateSession 处理耗时
func RecordCreateSessionDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if createSessionDuration != nil {
		createSessionDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordDefaultAgentSessionProvision records the best-effort provisioning and
// lazy-repair path. Callers must use the bounded trigger/outcome vocabulary.
func RecordDefaultAgentSessionProvision(ctx context.Context, trigger, outcome string) {
	if defaultAgentSessionProvision != nil {
		defaultAgentSessionProvision.Inc(ctx, metrics.L("trigger", trigger), metrics.L("outcome", outcome))
	}
}

// SetOutboxBacklog records the bounded pending batch observed by the relay.
func SetOutboxBacklog(ctx context.Context, count int) {
	if outboxBacklog != nil {
		outboxBacklog.Set(ctx, float64(count))
	}
}

// ============================================================================
// Logger 创建辅助函数
// ============================================================================

// NewLogger 创建带有 Trace Context 的 Logger
func NewLogger(cfg *clog.Config) (clog.Logger, error) {
	return clog.New(cfg, clog.WithTraceContext())
}
