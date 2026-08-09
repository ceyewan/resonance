// Package userauth carries a locally verified end-user identity inside Gateway.
// Roles and scopes are intentionally absent: Logic always reloads them from the
// authoritative identity repository.
package userauth

import (
	"context"
	"strconv"
	"strings"

	"github.com/ceyewan/genesis/auth"
)

type principalContextKey struct{}

type Principal struct {
	TenantID          string
	Username          string
	MembershipVersion int64
}

func FromClaims(claims *auth.Claims) (*Principal, bool) {
	if claims == nil || claims.Extra == nil || strings.TrimSpace(claims.Subject) == "" ||
		strings.TrimSpace(claims.Subject) != claims.Subject || len(claims.Subject) > 64 {
		return nil, false
	}
	tenantID, ok := claims.Extra["tenant_id"].(string)
	if !ok || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(tenantID) != tenantID || len(tenantID) > 64 {
		return nil, false
	}
	versionText, ok := claims.Extra["membership_version"].(string)
	if !ok {
		return nil, false
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version < 1 {
		return nil, false
	}
	return &Principal{TenantID: tenantID, Username: claims.Subject, MembershipVersion: version}, true
}

func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	if principal == nil {
		return ctx
	}
	copy := *principal
	return context.WithValue(ctx, principalContextKey{}, &copy)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(*Principal)
	if !ok || principal == nil || principal.TenantID == "" || principal.Username == "" || principal.MembershipVersion < 1 {
		return nil, false
	}
	copy := *principal
	return &copy, true
}
