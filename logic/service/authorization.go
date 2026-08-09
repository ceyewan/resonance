package service

import (
	"context"
	"errors"
	"slices"

	"github.com/ceyewan/resonance/model"
	sharediam "github.com/ceyewan/resonance/pkg/iam"
	"github.com/ceyewan/resonance/repo"
)

type IdentitySystemScopeAuthorizer struct {
	identityRepo repo.IdentityRepo
}

func NewIdentitySystemScopeAuthorizer(identityRepo repo.IdentityRepo) *IdentitySystemScopeAuthorizer {
	return &IdentitySystemScopeAuthorizer{identityRepo: identityRepo}
}

func (a *IdentitySystemScopeAuthorizer) HasSystemScope(ctx context.Context, tenantID, actorID, scope string) (bool, error) {
	if a == nil || a.identityRepo == nil || tenantID == "" || actorID == "" || scope == "" {
		return false, nil
	}
	authorization, err := a.identityRepo.ResolveTenantAuthorization(ctx, tenantID, actorID)
	if err != nil {
		if errors.Is(err, repo.ErrTenantMembershipNotFound) {
			return false, nil
		}
		return false, err
	}
	if authorization == nil || authorization.Membership == nil ||
		authorization.Membership.TenantID != tenantID || authorization.Membership.Username != actorID ||
		authorization.Membership.Status != model.TenantMembershipStatusActive ||
		authorization.Membership.Version < 1 {
		return false, nil
	}
	scopes, err := ScopesForSystemRoles(authorization.Roles)
	if err != nil {
		return false, err
	}
	if slices.Contains(scopes, scope) {
		return true, nil
	}
	return false, nil
}

// ScopesForSystemRoles 将权威系统角色映射为最小 Scope 集合。
// 未知角色会整体失败，避免配置错误被静默降级或产生含糊的授权结果。
func ScopesForSystemRoles(roles []string) ([]string, error) {
	return sharediam.ScopesForSystemRoles(roles)
}

func canonicalSystemRoles(roles []string) ([]string, error) {
	return sharediam.CanonicalSystemRoles(roles)
}
