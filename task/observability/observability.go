// Package observability 提供 Task 服务的可观测性支持
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
	ServiceName = "resonance-task"

	// TracerName Tracer 名称
	TracerName = "task-service"
)

var (
	// 全局组件
	meter    metrics.Meter
	initOnce sync.Once
	initErr  error
	shutdown func(context.Context) error

	// 业务指标
	storageProcessDuration metrics.Histogram
	pushEnqueueTotal       metrics.Counter
	pushEnqueueFailed      metrics.Counter
	pushProcessDuration    metrics.Histogram
	gatewayQueueDepth      metrics.Gauge
	gatewayConnected       metrics.Gauge
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
		metricsCfg.Port = 9090
	}
	if metricsCfg.Path == "" {
		metricsCfg.Path = "/metrics"
	}

	return metrics.New(metricsCfg)
}

// initBusinessMetrics 初始化业务指标
func initBusinessMetrics() {
	// Storage 处理耗时
	storageProcessDuration, _ = meter.Histogram(
		"task_storage_process_duration_seconds",
		"Storage consumer message processing duration",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}),
	)

	// Push 入队成功总数
	pushEnqueueTotal, _ = meter.Counter(
		"task_push_enqueue_total",
		"Total number of push tasks enqueued",
	)

	// Push 入队失败总数
	pushEnqueueFailed, _ = meter.Counter(
		"task_push_enqueue_failed_total",
		"Total number of push tasks failed to enqueue",
	)

	// Push 处理耗时
	pushProcessDuration, _ = meter.Histogram(
		"task_push_process_duration_seconds",
		"Push consumer message processing duration",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}),
	)

	// Gateway 队列深度
	gatewayQueueDepth, _ = meter.Gauge(
		"task_gateway_queue_depth",
		"Current depth of gateway push queue",
	)

	// Gateway 连接数
	gatewayConnected, _ = meter.Gauge(
		"task_gateway_connected_total",
		"Total number of connected gateway clients",
	)
}

// ============================================================================
// Trace 辅助函数
// ============================================================================

// StartSpan 开始一个新的 Span
// 返回带有 Span 的 Context 和结束函数
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
// 用于 MQ 消费者场景，还原上游的链路追踪信息
func ExtractTraceContext(ctx context.Context, traceHeaders map[string]string) context.Context {
	if len(traceHeaders) == 0 {
		return ctx
	}
	return genesistrace.Extract(ctx, traceHeaders)
}

// HasTraceContext 检查 Context 中是否包含有效的 Trace Context
// 用于判断是否需要从备用来源提取 Trace 信息
func HasTraceContext(ctx context.Context) bool {
	_, span := otel.GetTracerProvider().Tracer(TracerName).Start(ctx, "dummy")
	defer span.End()
	return span.SpanContext().IsValid() && span.SpanContext().HasTraceID()
}

// InjectTraceContext 将当前 Context 的 Trace 信息注入到 map
// 用于 MQ 生产者场景，将链路追踪信息传递给下游
func InjectTraceContext(ctx context.Context, carrier map[string]string) {
	if carrier == nil {
		return
	}
	genesistrace.Inject(ctx, carrier)
}

// ============================================================================
// Metrics 记录函数
// ============================================================================

// RecordStorageProcess 记录 Storage 处理耗时
func RecordStorageProcess(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if storageProcessDuration != nil {
		storageProcessDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordPushEnqueue 记录 Push 入队成功
func RecordPushEnqueue(ctx context.Context, labels ...metrics.Label) {
	if pushEnqueueTotal != nil {
		pushEnqueueTotal.Inc(ctx, labels...)
	}
}

// RecordPushEnqueueFailed 记录 Push 入队失败
func RecordPushEnqueueFailed(ctx context.Context, labels ...metrics.Label) {
	if pushEnqueueFailed != nil {
		pushEnqueueFailed.Inc(ctx, labels...)
	}
}

// RecordPushProcess 记录 Push 处理耗时
func RecordPushProcess(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if pushProcessDuration != nil {
		pushProcessDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// SetGatewayQueueDepth 设置 Gateway 队列深度
func SetGatewayQueueDepth(ctx context.Context, gatewayID string, depth int) {
	if gatewayQueueDepth != nil {
		gatewayQueueDepth.Set(ctx, float64(depth), metrics.L("gateway_id", gatewayID))
	}
}

// SetGatewayConnected 设置 Gateway 连接数
func SetGatewayConnected(ctx context.Context, count int) {
	if gatewayConnected != nil {
		gatewayConnected.Set(ctx, float64(count))
	}
}

// ============================================================================
// Logger 创建辅助函数
// ============================================================================

// NewLogger 创建带有 Trace Context 的 Logger
func NewLogger(cfg *clog.Config) (clog.Logger, error) {
	return clog.New(cfg, clog.WithTraceContext())
}
