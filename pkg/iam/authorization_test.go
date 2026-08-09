package iam

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestScopesForSystemRoles_CanonicalAndFailClosed(t *testing.T) {
	scopes, err := ScopesForSystemRoles([]string{
		model.SystemRoleUser, model.SystemRoleIAMAdmin, model.SystemRoleUser,
	})
	require.NoError(t, err)
	require.Contains(t, scopes, model.ScopeChatUse)
	require.Contains(t, scopes, model.ScopeIAMUsersRead)
	require.Contains(t, scopes, model.ScopeAgentApprovalDecide)

	_, err = ScopesForSystemRoles([]string{model.SystemRoleUser, "unknown"})
	require.Error(t, err)
}

func TestAuthorizeAgentProfile_RequiresCurrentRoleAndScope(t *testing.T) {
	require.NoError(t, AuthorizeAgentProfile(
		model.AgentProfileUserAssistant,
		[]string{model.SystemRoleUser},
		[]string{model.ScopeChatUse},
	))
	require.ErrorIs(t, AuthorizeAgentProfile(
		model.AgentProfileUserAssistant,
		[]string{model.SystemRoleUser},
		nil,
	), ErrAgentProfileUnauthorized)

	require.NoError(t, AuthorizeAgentProfile(
		model.AgentProfileIAMAdmin,
		[]string{model.SystemRoleIAMAdmin},
		[]string{model.ScopeIAMUsersRead},
	))
	require.ErrorIs(t, AuthorizeAgentProfile(
		model.AgentProfileIAMAdmin,
		[]string{model.SystemRoleUser},
		[]string{model.ScopeIAMUsersRead},
	), ErrAgentProfileUnauthorized)
	require.ErrorIs(t, AuthorizeAgentProfile(
		"future-profile", nil, nil,
	), ErrAgentProfileUnauthorized)
}
