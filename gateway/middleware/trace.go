package middleware

const (
	// TraceIDKey Context 中 trace_id 的键
	TraceIDKey = "trace_id"
	// TraceIDHeader HTTP header 中 trace_id 的键
	TraceIDHeader = "X-Trace-ID"
	// TraceIDMetadata gRPC metadata 中 trace_id 的键
	TraceIDMetadata = "trace-id"
)
