package middleware

import (
	"context"

	"github.com/ceyewan/resonance/gateway/observability"
)

const (
	// TraceIDKey Context 中 trace_id 的键
	TraceIDKey = "trace_id"
	// TraceIDHeader HTTP header 中 trace_id 的键
	TraceIDHeader = "X-Trace-ID"
	// TraceIDMetadata gRPC metadata 中 trace_id 的键（符合 W3C Trace Context 标准）
	TraceIDMetadata = "traceparent"
)

// GetTraceID 从 Context 中获取 TraceID（优先使用 OTEL TraceID）
// 降级策略：
// 1. 尝试从 Context 中获取已有的 traceID
// 2. 如果没有，从 OTEL 生成新的 TraceID
func GetTraceID(ctx context.Context) string {
	// 尝试从 Context 中获取已有的 TraceID
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok && traceID != "" {
		return traceID
	}
	// 从 OTEL 生成新的 TraceID
	return observability.GetTraceID(ctx)
}
