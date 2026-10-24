package identity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

type identityUsers struct{ kind int }

func (u identityUsers) GetUserByUsername(_ context.Context, name string) (*model.User, error) {
	return &model.User{Username: name, Kind: u.kind}, nil
}
func TestPilotUsesHumanIdentityAcrossTenants(t *testing.T) {
	r, err := NewPrincipalResolver(identityUsers{})
	require.NoError(t, err)
	a, err := r.ResolvePrincipal(context.Background(), "tenant-a", "alice")
	require.NoError(t, err)
	b, err := r.ResolvePrincipal(context.Background(), "tenant-b", "bob")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	bot, _ := NewPrincipalResolver(identityUsers{kind: model.UserKindAgentBot})
	_, err = bot.ResolvePrincipal(context.Background(), "tenant-a", "pilot")
	require.ErrorIs(t, err, ErrPrincipalUnauthorized)
}
