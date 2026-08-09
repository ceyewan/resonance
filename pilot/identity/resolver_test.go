package identity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestResolvers_FailClosedAcrossTenantAndIdentityKind(t *testing.T) {
	profile := pilotruntime.ProfileSnapshot{ID: "user", Version: 1, Provider: "anthropic", Model: "model", SystemPrompt: "safe"}
	profiles, err := NewStaticProfileResolver("tenant-a", profile)
	require.NoError(t, err)
	resolved, err := profiles.ResolveProfile(context.Background(), "tenant-a", "user", 1)
	require.NoError(t, err)
	require.Equal(t, profile, resolved)
	_, err = profiles.ResolveProfile(context.Background(), "tenant-b", "user", 1)
	require.Error(t, err)

	users := &identityUsers{values: map[string]*model.User{
		"alice": {Username: "alice", Kind: model.UserKindHuman},
		"agent": {Username: "agent", Kind: model.UserKindAgentBot},
	}}
	principals, err := NewSingleTenantPrincipalResolver("tenant-a", users)
	require.NoError(t, err)
	principal, err := principals.ResolvePrincipal(context.Background(), "tenant-a", "alice")
	require.NoError(t, err)
	require.Equal(t, "alice", principal.ActorID)
	_, err = principals.ResolvePrincipal(context.Background(), "tenant-b", "alice")
	require.Error(t, err)
	_, err = principals.ResolvePrincipal(context.Background(), "tenant-a", "agent")
	require.Error(t, err)
}

type identityUsers struct{ values map[string]*model.User }

func (u *identityUsers) GetUserByUsername(_ context.Context, username string) (*model.User, error) {
	return u.values[username], nil
}
