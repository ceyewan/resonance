package repo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestPilotConversationIsUniquePerTenantAndOwner(t *testing.T) {
	database := newHistorySQLiteDB(t)
	conversations, err := NewSessionRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	open := func(id, tenant, owner string, version int64) (*model.Session, bool, error) {
		return conversations.CreateSessionWithMembers(ctx, &model.Session{SessionID: id, TenantID: tenant, OwnerUsername: owner, Kind: model.SessionKindAI, Type: 1, ProfileID: model.AgentProfileUserAssistant, ProfileVersion: version}, []*model.SessionMember{{SessionID: id, Username: owner}, {SessionID: id, Username: "pilot"}})
	}
	first, created, err := open("alice-chat", "tenant-a", "alice", 1)
	require.NoError(t, err)
	require.True(t, created)
	repeated, created, err := open("another-id", "tenant-a", "alice", 2)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.SessionID, repeated.SessionID)
	other, created, err := open("bob-chat", "tenant-a", "bob", 1)
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, first.SessionID, other.SessionID)
	_, created, err = open("tenant-b-chat", "tenant-b", "alice", 1)
	require.NoError(t, err)
	require.True(t, created)
	require.Error(t, conversations.AddMember(ctx, &model.SessionMember{SessionID: first.SessionID, Username: "third-person"}))
}
