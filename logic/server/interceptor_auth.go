package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

const (
	usernameMetadataKey = "x-username"
)

func (s *GRPCServer) authUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	username := ""
	if serviceauth.HasIncomingCredentials(ctx) {
		message, ok := req.(proto.Message)
		if !ok || s.serviceAuth == nil {
			return nil, status.Error(codes.Unauthenticated, "invalid service credentials")
		}
		claims, err := s.serviceAuth.VerifyIncoming(ctx, info.FullMethod, message)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid service credentials")
		}
		if claims.ServiceID == s.gatewayServiceID {
			if claims.PrincipalVersion < 1 || s.userPrincipalResolver == nil {
				return nil, status.Error(codes.Unauthenticated, "user principal resolution unavailable")
			}
			principal, resolveErr := s.userPrincipalResolver.ResolveUserPrincipal(ctx, claims.TenantID, claims.Actor)
			if resolveErr != nil || principal == nil ||
				principal.TenantID != claims.TenantID || principal.Username != claims.Actor ||
				principal.Version != claims.PrincipalVersion {
				return nil, status.Error(codes.Unauthenticated, "stale or invalid user principal")
			}
			ctx = service.WithUserPrincipal(ctx, principal)
		} else {
			if profile, ok := s.serviceProfiles[claims.ServiceID]; ok {
				ctx = service.WithProfiledServicePrincipal(
					ctx, claims.ServiceID, claims.TenantID, claims.Actor,
					profile.ProfileID, profile.ProfileVersion,
				)
			} else {
				ctx = service.WithServicePrincipal(ctx, claims.ServiceID, claims.TenantID, claims.Actor)
			}
		}
		username = claims.Actor
	} else if md, ok := metadata.FromIncomingContext(ctx); ok {
		if len(md.Get("authorization")) > 0 || len(md.Get("x-tenant-id")) > 0 ||
			len(md.Get("x-roles")) > 0 || len(md.Get("x-scopes")) > 0 {
			return nil, status.Error(codes.Unauthenticated, "untrusted principal metadata")
		}
		values := md.Get(usernameMetadataKey)
		if len(values) > 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid username metadata")
		}
		if len(values) == 1 {
			if !s.allowLegacyUsernameAuth {
				return nil, status.Error(codes.Unauthenticated, "legacy username authentication is disabled")
			}
			username = values[0]
			if _, protected := s.protectedActors[username]; protected {
				return nil, status.Error(codes.Unauthenticated, "protected actor requires service credentials")
			}
		}
	}

	if shouldRequireUsername(info.FullMethod) && username == "" {
		return nil, status.Errorf(codes.Unauthenticated, "missing username metadata")
	}
	if username != "" {
		if _, userPrincipal := service.UserPrincipalFromCtx(ctx); !userPrincipal {
			ctx = service.WithUsername(ctx, username)
		}
	}
	return handler(ctx, req)
}

func shouldRequireUsername(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/resonance.logic.v1.ChatService/") ||
		strings.HasPrefix(fullMethod, "/resonance.logic.v1.SessionService/") ||
		strings.HasPrefix(fullMethod, "/resonance.logic.v1.AgentApprovalService/")
}
