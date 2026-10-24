package iam

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestIMRolesAreCanonicalAndUnknownRolesFailClosed(t *testing.T) {
	roles, err := CanonicalSystemRoles([]string{model.SystemRoleUser, model.SystemRoleUser})
	require.NoError(t, err)
	require.Equal(t, []string{model.SystemRoleUser}, roles)
	_, err = ScopesForSystemRoles([]string{"unknown"})
	require.Error(t, err)
}
