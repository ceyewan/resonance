package service

import sharediam "github.com/ceyewan/resonance/pkg/iam"

func ScopesForSystemRoles(roles []string) ([]string, error) {
	return sharediam.ScopesForSystemRoles(roles)
}
func canonicalSystemRoles(roles []string) ([]string, error) {
	return sharediam.CanonicalSystemRoles(roles)
}
