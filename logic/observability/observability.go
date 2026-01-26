// Package observability 提供 Logic 服务的可观测性支持
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
	ServiceName = "resonance-logic"

	// TracerName Tracer 名称
	TracerName = "logic-service"
)

var (
	// 全局组件
	meter     metrics.Meter
	traceOnce sync.Once
	shutdown  func(context.Context) error

	// 业务指标
	loginDuration         metrics.Histogram
	registerDuration      metrics.Histogram
	sendMessageDuration   metrics.Histogram
	createSessionDuration metrics.Histogram
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

// ============================================================================
// Logger 创建辅助函数
// ============================================================================

// NewLogger 创建带有 Trace Context 的 Logger
func NewLogger(cfg *clog.Config) (clog.Logger, error) {
	return clog.New(cfg, clog.WithTraceContext())
}
