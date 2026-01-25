// Package observability 提供 Task 服务的可观测性支持
// 包括 Trace（分布式追踪）和 Metrics（指标收集）
package observability

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
)

const (
	// ServiceName 服务名称
	ServiceName = "resonance-task"

	// TracerName Tracer 名称
	TracerName = "task-service"
)

var (
	// 全局组件
	meter     metrics.Meter
	traceOnce sync.Once
	shutdown  func(context.Context) error

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
	var initErr error

	traceOnce.Do(func() {
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
			initErr = fmt.Errorf("init metrics: %w", err)
			return
		}

		// 3. 初始化业务指标
		initBusinessMetrics()
	})

	return initErr
}

// Shutdown 优雅关闭
func Shutdown(ctx context.Context) error {
	if shutdown != nil {
		return shutdown(ctx)
	}
	if meter != nil {
		return meter.Shutdown(ctx)
	}
	return nil
}

// initTrace 初始化 Trace
// TODO: 等待 Genesis trace 包更新后，切换为 trace.Init
func initTrace(cfg *Config) (func(context.Context) error, error) {
	if cfg.Trace.Disable {
		// 禁用 Trace，只生成 TraceID 不上报
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String(ServiceName),
			)),
		)
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return tp.Shutdown, nil
	}

	// 配置 OTLP Exporter
	endpoint := cfg.Trace.Endpoint
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	sampler := cfg.Trace.Sampler
	if sampler == 0 {
		sampler = 1.0
	}

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithTimeout(5 * time.Second),
	}
	if cfg.Trace.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	ctx := context.Background()
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampler))),
	}
	tpOpts = append(tpOpts, sdktrace.WithBatcher(exporter))

	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// initMetrics 初始化 Metrics
func initMetrics(cfg *Config) (metrics.Meter, error) {
	metricsCfg := &metrics.Config{
		ServiceName:   ServiceName,
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
// TODO: 等待 Genesis trace 包更新后，切换为 trace.Extract
func ExtractTraceContext(ctx context.Context, traceHeaders map[string]string) context.Context {
	if len(traceHeaders) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(traceHeaders))
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
// TODO: 等待 Genesis trace 包更新后，切换为 trace.Inject
func InjectTraceContext(ctx context.Context, carrier map[string]string) {
	if len(carrier) == 0 {
		// 确保 carrier 不为 nil，但不要覆盖已有的非空 map
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier))
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
