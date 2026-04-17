package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const metadataUsernameKey = "x-username"

func usernameFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Errorf(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get(metadataUsernameKey)
	if len(values) == 0 || values[0] == "" {
		return "", status.Errorf(codes.Unauthenticated, "missing username metadata")
	}
	return values[0], nil
}
