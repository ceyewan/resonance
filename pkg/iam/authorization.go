// Package iam defines the minimal IM login permissions. Pilot has no IAM tools.
package iam

import (
	"fmt"
	"slices"

	"github.com/ceyewan/resonance/model"
)

func CanonicalSystemRoles(roles []string) ([]string, error) {
	for _, role := range roles {
		if role != model.SystemRoleUser {
			return nil, fmt.Errorf("unknown system role %q", role)
		}
	}
	if len(roles) == 0 {
		return nil, nil
	}
	return []string{model.SystemRoleUser}, nil
}

func ScopesForSystemRoles(roles []string) ([]string, error) {
	canonical, err := CanonicalSystemRoles(roles)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(canonical, model.SystemRoleUser) {
		return nil, nil
	}
	return []string{model.ScopeChatUse, model.ScopeProfileSelfRead}, nil
}
