package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ceyewan/resonance/logic/service"
)

const usernameMetadataKey = "x-username"

func (s *GRPCServer) authUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	username := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		values := md.Get(usernameMetadataKey)
		if len(values) > 0 {
			username = values[0]
		}
	}

	if shouldRequireUsername(info.FullMethod) && username == "" {
		return nil, status.Errorf(codes.Unauthenticated, "missing username metadata")
	}
	if username != "" {
		ctx = service.WithUsername(ctx, username)
	}
	return handler(ctx, req)
}

func shouldRequireUsername(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/resonance.logic.v1.ChatService/") ||
		strings.HasPrefix(fullMethod, "/resonance.logic.v1.SessionService/")
}
