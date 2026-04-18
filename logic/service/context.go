package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const metadataUsernameKey = "x-username"

type usernameCtxKey struct{}

func WithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, usernameCtxKey{}, username)
}

func UsernameFromCtx(ctx context.Context) (string, bool) {
	if username, ok := ctx.Value(usernameCtxKey{}).(string); ok && username != "" {
		return username, true
	}
	return "", false
}

func MustUsernameFromCtx(ctx context.Context) (string, error) {
	if username, ok := UsernameFromCtx(ctx); ok {
		return username, nil
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		values := md.Get(metadataUsernameKey)
		if len(values) > 0 && values[0] != "" {
			return values[0], nil
		}
	}
	return "", status.Errorf(codes.Unauthenticated, "missing username in context")
}
