// Package iam contains the shared, fail-closed mapping from authoritative
// system roles to application scopes. Session roles must never enter this map.
package iam

import (
	"errors"
	"fmt"
	"sort"

	"github.com/ceyewan/resonance/model"
)

var ErrAgentProfileUnauthorized = errors.New("principal is not authorized for agent profile")

var systemRoleScopes = map[string][]string{
	model.SystemRoleUser: {
		model.ScopeChatUse,
		model.ScopeProfileSelfRead,
	},
	model.SystemRoleIAMAdmin: {
		model.ScopeAgentApprovalDecide,
		model.ScopeAgentApprovalRead,
		model.ScopeIAMRolesRead,
		model.ScopeIAMRolesWrite,
		model.ScopeIAMUsersRead,
		model.ScopeIAMUsersWrite,
	},
}

// ScopesForSystemRoles returns a canonical scope set. Any unknown role rejects
// the whole authorization snapshot so configuration drift cannot add access.
func ScopesForSystemRoles(roles []string) ([]string, error) {
	canonical, err := CanonicalSystemRoles(roles)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, role := range canonical {
		for _, scope := range systemRoleScopes[role] {
			seen[scope] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for scope := range seen {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result, nil
}

func CanonicalSystemRoles(roles []string) ([]string, error) {
	seen := make(map[string]struct{}, len(roles))
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		if _, ok := systemRoleScopes[role]; !ok {
			return nil, fmt.Errorf("unknown system role %q", role)
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	sort.Strings(result)
	return result, nil
}

// AuthorizeAgentProfile applies the same minimum Role/Scope boundary used by
// Logic when an AI conversation is created. It must be re-evaluated at ingress
// and immediately before a durable Run resumes an existing Session.
func AuthorizeAgentProfile(profileID string, roles, scopes []string) error {
	requiredRole := ""
	requiredScope := ""
	switch profileID {
	case model.AgentProfileUserAssistant:
		requiredScope = model.ScopeChatUse
	case model.AgentProfileIAMAdmin:
		requiredRole = model.SystemRoleIAMAdmin
		requiredScope = model.ScopeIAMUsersRead
	default:
		return fmt.Errorf("%w: unknown profile %q", ErrAgentProfileUnauthorized, profileID)
	}
	if requiredRole != "" && !contains(roles, requiredRole) {
		return fmt.Errorf("%w: required role is absent", ErrAgentProfileUnauthorized)
	}
	if !contains(scopes, requiredScope) {
		return fmt.Errorf("%w: required scope is absent", ErrAgentProfileUnauthorized)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
