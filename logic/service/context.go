package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const metadataUsernameKey = "x-username"

type usernameCtxKey struct{}
type tenantCtxKey struct{}
type serviceIDCtxKey struct{}
type serviceProfileCtxKey struct{}
type userPrincipalCtxKey struct{}

type UserPrincipal struct {
	TenantID string
	Username string
	Version  int64
	Roles    []string
	Scopes   []string
}

type ServiceProfile struct {
	ProfileID      string
	ProfileVersion int64
}

func WithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, usernameCtxKey{}, username)
}

func WithServicePrincipal(ctx context.Context, serviceID, tenantID, actor string) context.Context {
	ctx = context.WithValue(ctx, serviceIDCtxKey{}, serviceID)
	ctx = context.WithValue(ctx, tenantCtxKey{}, tenantID)
	return WithUsername(ctx, actor)
}

func WithProfiledServicePrincipal(
	ctx context.Context,
	serviceID, tenantID, actor, profileID string,
	profileVersion int64,
) context.Context {
	ctx = WithServicePrincipal(ctx, serviceID, tenantID, actor)
	return context.WithValue(ctx, serviceProfileCtxKey{}, ServiceProfile{
		ProfileID: profileID, ProfileVersion: profileVersion,
	})
}

func WithUserPrincipal(ctx context.Context, principal *UserPrincipal) context.Context {
	if principal == nil {
		return ctx
	}
	copy := &UserPrincipal{
		TenantID: principal.TenantID,
		Username: principal.Username,
		Version:  principal.Version,
		Roles:    append([]string(nil), principal.Roles...),
		Scopes:   append([]string(nil), principal.Scopes...),
	}
	ctx = context.WithValue(ctx, userPrincipalCtxKey{}, copy)
	ctx = context.WithValue(ctx, tenantCtxKey{}, copy.TenantID)
	return WithUsername(ctx, copy.Username)
}

func UserPrincipalFromCtx(ctx context.Context) (*UserPrincipal, bool) {
	principal, ok := ctx.Value(userPrincipalCtxKey{}).(*UserPrincipal)
	if !ok || principal == nil || principal.TenantID == "" || principal.Username == "" {
		return nil, false
	}
	return &UserPrincipal{
		TenantID: principal.TenantID,
		Username: principal.Username,
		Version:  principal.Version,
		Roles:    append([]string(nil), principal.Roles...),
		Scopes:   append([]string(nil), principal.Scopes...),
	}, true
}

func TenantFromCtx(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(tenantCtxKey{}).(string)
	return tenantID, ok && tenantID != ""
}

func ServicePrincipalFromCtx(ctx context.Context) (serviceID, tenantID string, ok bool) {
	serviceID, serviceOK := ctx.Value(serviceIDCtxKey{}).(string)
	tenantID, tenantOK := ctx.Value(tenantCtxKey{}).(string)
	return serviceID, tenantID, serviceOK && tenantOK && serviceID != "" && tenantID != ""
}

func ServiceProfileFromCtx(ctx context.Context) (ServiceProfile, bool) {
	profile, ok := ctx.Value(serviceProfileCtxKey{}).(ServiceProfile)
	return profile, ok && profile.ProfileID != "" && profile.ProfileVersion > 0
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
