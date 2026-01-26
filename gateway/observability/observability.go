// Package observability 提供 Gateway 服务的可观测性支持
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
	ServiceName = "resonance-gateway"

	// TracerName Tracer 名称
	TracerName = "gateway-service"
)

var (
	// 全局组件
	meter     metrics.Meter
	traceOnce sync.Once
	shutdown  func(context.Context) error

	// 业务指标 - WebSocket
	websocketConnectionsActive metrics.Gauge
	websocketConnectionsTotal  metrics.Counter

	// 业务指标 - 消息处理
	messagesPulseTotal    metrics.Counter
	messagesReceivedTotal metrics.Counter
	messagesSentTotal     metrics.Counter

	// 业务指标 - 推送
	pushDuration metrics.Histogram
	pushFailed   metrics.Counter

	// 业务指标 - HTTP
	httpRequestsTotal   metrics.Counter
	httpRequestDuration metrics.Histogram
	httpErrorsTotal     metrics.Counter

	// 业务指标 - gRPC (Push Service)
	grpcRequestsTotal   metrics.Counter
	grpcRequestDuration metrics.Histogram
	grpcErrorsTotal     metrics.Counter
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
		metricsCfg.Port = 9092
	}
	if metricsCfg.Path == "" {
		metricsCfg.Path = "/metrics"
	}

	return metrics.New(metricsCfg)
}

// initBusinessMetrics 初始化业务指标
func initBusinessMetrics() {
	// WebSocket 连接数（当前）
	websocketConnectionsActive, _ = meter.Gauge(
		"gateway_websocket_connections_active",
		"Current number of active WebSocket connections",
	)

	// WebSocket 连接总数
	websocketConnectionsTotal, _ = meter.Counter(
		"gateway_websocket_connections_total",
		"Total number of WebSocket connections established",
	)

	// 心跳消息总数
	messagesPulseTotal, _ = meter.Counter(
		"gateway_messages_pulse_total",
		"Total number of pulse messages received",
	)

	// 聊天消息总数
	messagesReceivedTotal, _ = meter.Counter(
		"gateway_messages_received_total",
		"Total number of chat messages received",
	)

	// 推送消息总数
	messagesSentTotal, _ = meter.Counter(
		"gateway_messages_sent_total",
		"Total number of messages sent via push",
	)

	// 推送延迟（秒）
	pushDuration, _ = meter.Histogram(
		"gateway_push_duration_seconds",
		"Message push latency",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}),
	)

	// 推送失败总数
	pushFailed, _ = meter.Counter(
		"gateway_push_failed_total",
		"Total number of failed push messages",
	)

	// HTTP 请求总数
	httpRequestsTotal, _ = meter.Counter(
		"gateway_http_requests_total",
		"Total number of HTTP requests",
	)

	// HTTP 请求延迟（秒）
	httpRequestDuration, _ = meter.Histogram(
		"gateway_http_request_duration_seconds",
		"HTTP request latency",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}),
	)

	// HTTP 错误总数
	httpErrorsTotal, _ = meter.Counter(
		"gateway_http_errors_total",
		"Total number of HTTP errors",
	)

	// gRPC 请求总数
	grpcRequestsTotal, _ = meter.Counter(
		"gateway_grpc_requests_total",
		"Total number of gRPC requests",
	)

	// gRPC 请求延迟（秒）
	grpcRequestDuration, _ = meter.Histogram(
		"gateway_grpc_request_duration_seconds",
		"gRPC request latency",
		metrics.WithUnit("s"),
		metrics.WithBuckets([]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5}),
	)

	// gRPC 错误总数
	grpcErrorsTotal, _ = meter.Counter(
		"gateway_grpc_errors_total",
		"Total number of gRPC errors",
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
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(traceHeaders))
}

// InjectTraceContext 将当前 Context 的 Trace 信息注入到 map
func InjectTraceContext(ctx context.Context, carrier map[string]string) {
	if carrier == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier))
}

// ============================================================================
// Metrics 记录函数 - WebSocket
// ============================================================================

// SetWebSocketConnectionsActive 设置当前活跃的 WebSocket 连接数
func SetWebSocketConnectionsActive(ctx context.Context, count int) {
	if websocketConnectionsActive != nil {
		websocketConnectionsActive.Set(ctx, float64(count))
	}
}

// RecordWebSocketConnectionEstablished 记录新建 WebSocket 连接
func RecordWebSocketConnectionEstablished(ctx context.Context) {
	if websocketConnectionsTotal != nil {
		websocketConnectionsTotal.Inc(ctx)
	}
}

// ============================================================================
// Metrics 记录函数 - 消息处理
// ============================================================================

// RecordMessagePulse 记录心跳消息
func RecordMessagePulse(ctx context.Context) {
	if messagesPulseTotal != nil {
		messagesPulseTotal.Inc(ctx)
	}
}

// RecordMessageReceived 记录接收的聊天消息
func RecordMessageReceived(ctx context.Context) {
	if messagesReceivedTotal != nil {
		messagesReceivedTotal.Inc(ctx)
	}
}

// RecordMessageSent 记录发送的消息
func RecordMessageSent(ctx context.Context, count int, labels ...metrics.Label) {
	if messagesSentTotal != nil {
		for i := 0; i < count; i++ {
			messagesSentTotal.Inc(ctx, labels...)
		}
	}
}

// ============================================================================
// Metrics 记录函数 - 推送
// ============================================================================

// RecordPushDuration 记录推送延迟
func RecordPushDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if pushDuration != nil {
		pushDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordPushFailed 记录推送失败
func RecordPushFailed(ctx context.Context, count int, labels ...metrics.Label) {
	if pushFailed != nil {
		for i := 0; i < count; i++ {
			pushFailed.Inc(ctx, labels...)
		}
	}
}

// ============================================================================
// Metrics 记录函数 - HTTP
// ============================================================================

// RecordHTTPRequest 记录 HTTP 请求
func RecordHTTPRequest(ctx context.Context) {
	if httpRequestsTotal != nil {
		httpRequestsTotal.Inc(ctx)
	}
}

// RecordHTTPRequestDuration 记录 HTTP 请求延迟
func RecordHTTPRequestDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if httpRequestDuration != nil {
		httpRequestDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordHTTPError 记录 HTTP 错误
func RecordHTTPError(ctx context.Context, labels ...metrics.Label) {
	if httpErrorsTotal != nil {
		httpErrorsTotal.Inc(ctx, labels...)
	}
}

// ============================================================================
// Metrics 记录函数 - gRPC
// ============================================================================

// RecordGRPCRequest 记录 gRPC 请求
func RecordGRPCRequest(ctx context.Context) {
	if grpcRequestsTotal != nil {
		grpcRequestsTotal.Inc(ctx)
	}
}

// RecordGRPCRequestDuration 记录 gRPC 请求延迟
func RecordGRPCRequestDuration(ctx context.Context, duration time.Duration, labels ...metrics.Label) {
	if grpcRequestDuration != nil {
		grpcRequestDuration.Record(ctx, duration.Seconds(), labels...)
	}
}

// RecordGRPCError 记录 gRPC 错误
func RecordGRPCError(ctx context.Context, labels ...metrics.Label) {
	if grpcErrorsTotal != nil {
		grpcErrorsTotal.Inc(ctx, labels...)
	}
}

// ============================================================================
// Logger 创建辅助函数
// ============================================================================

// NewLogger 创建带有 Trace Context 的 Logger
func NewLogger(cfg *clog.Config) (clog.Logger, error) {
	return clog.New(cfg, clog.WithTraceContext())
}
