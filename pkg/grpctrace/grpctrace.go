// Package grpctrace propagates the W3C Trace Context across gRPC boundaries.
package grpctrace

import (
	"context"

	genesistrace "github.com/ceyewan/genesis/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	traceParent = "traceparent"
	traceState  = "tracestate"
)

// InjectOutgoing appends the current W3C Trace Context to outgoing metadata.
func InjectOutgoing(ctx context.Context) context.Context {
	carrier := make(map[string]string, 2)
	genesistrace.Inject(ctx, carrier)
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	for _, key := range []string{traceParent, traceState} {
		md.Delete(key)
		if value := carrier[key]; value != "" {
			md.Set(key, value)
		}
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// ExtractIncoming restores the W3C Trace Context from incoming metadata.
func ExtractIncoming(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	carrier := make(map[string]string, 2)
	for _, key := range []string{traceParent, traceState} {
		if values := md.Get(key); len(values) > 0 && values[0] != "" {
			carrier[key] = values[0]
		}
	}
	if len(carrier) == 0 {
		return ctx
	}
	return genesistrace.Extract(ctx, carrier)
}

func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		return invoker(InjectOutgoing(ctx), method, req, reply, connection, options...)
	}
}

func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, description *grpc.StreamDesc, connection *grpc.ClientConn, method string, streamer grpc.Streamer, options ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(InjectOutgoing(ctx), description, connection, method, options...)
	}
}

func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(ExtractIncoming(ctx), request)
	}
}

func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(server, &serverStream{ServerStream: stream, ctx: ExtractIncoming(stream.Context())})
	}
}

type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context { return s.ctx }
