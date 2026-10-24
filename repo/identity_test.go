package repo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestIdentityRepo_CreateIdentityAndResolveTenantScoped(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()
	identities, err := NewIdentityRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	user := &model.User{Username: "identity-alice", Password: "hashed", Kind: model.UserKindHuman}
	membership := &model.TenantMembership{TenantID: "tenant-a", Username: user.Username, Status: model.TenantMembershipStatusActive, Version: 1}
	require.NoError(t, identities.CreateIdentity(ctx, user, membership, []string{model.SystemRoleUser}))
	current, err := identities.ResolveTenantAuthorization(ctx, "tenant-a", user.Username)
	require.NoError(t, err)
	require.Equal(t, []string{model.SystemRoleUser}, current.Roles)
	_, err = identities.ResolveTenantAuthorization(ctx, "tenant-b", user.Username)
	require.ErrorIs(t, err, ErrTenantMembershipNotFound)
	_, err = identities.UpdateTenantMembershipStatus(ctx, "tenant-a", user.Username, model.TenantMembershipStatusDisabled, 1)
	require.NoError(t, err)
	changed, err := identities.ResolveTenantAuthorization(ctx, "tenant-a", user.Username)
	require.NoError(t, err)
	require.Equal(t, model.TenantMembershipStatusDisabled, changed.Membership.Status)
	require.Greater(t, changed.Membership.Version, current.Membership.Version)
}
