package identity

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestAuthoritativePrincipalResolver_ReloadsRolesAndFailsClosed(t *testing.T) {
	users := &authoritativeUsers{values: map[string]*model.User{
		"alice": {Username: "alice", Nickname: "Alice", Kind: model.UserKindHuman},
	}}
	identities := &authoritativeIdentities{authorizations: map[string]*repo.TenantAuthorization{
		"tenant-a/alice": authorization("tenant-a", "alice", model.TenantMembershipStatusActive, model.SystemRoleIAMAdmin),
	}}
	resolver, err := NewAuthoritativePrincipalResolver("tenant-a", users, identities)
	require.NoError(t, err)

	principal, err := resolver.ResolvePrincipal(context.Background(), "tenant-a", "alice")
	require.NoError(t, err)
	require.Equal(t, []string{model.SystemRoleIAMAdmin}, principal.Roles)
	require.Contains(t, principal.Scopes, model.ScopeIAMUsersRead)

	identities.authorizations["tenant-a/alice"].Membership.Status = model.TenantMembershipStatusDisabled
	_, err = resolver.ResolvePrincipal(context.Background(), "tenant-a", "alice")
	require.ErrorIs(t, err, ErrPrincipalUnauthorized, "a downgrade must take effect on the next resolution")
	_, err = resolver.ResolvePrincipal(context.Background(), "tenant-b", "alice")
	require.Error(t, err)

	identities.authorizations["tenant-a/alice"] = authorization(
		"tenant-a", "alice", model.TenantMembershipStatusActive, "unknown-role",
	)
	_, err = resolver.ResolvePrincipal(context.Background(), "tenant-a", "alice")
	require.ErrorIs(t, err, ErrPrincipalUnauthorized, "unknown roles must fail closed")
}

func TestAuthoritativePrincipalResolver_AuthorizeProfileRejectsDowngrade(t *testing.T) {
	users := &authoritativeUsers{values: map[string]*model.User{
		"alice": {Username: "alice", Kind: model.UserKindHuman},
	}}
	identities := &authoritativeIdentities{authorizations: map[string]*repo.TenantAuthorization{
		"tenant-a/alice": authorization("tenant-a", "alice", model.TenantMembershipStatusActive, model.SystemRoleIAMAdmin),
	}}
	resolver, err := NewAuthoritativePrincipalResolver("tenant-a", users, identities)
	require.NoError(t, err)

	allowed, err := resolver.AuthorizeProfile(context.Background(), "tenant-a", "alice", model.AgentProfileIAMAdmin)
	require.NoError(t, err)
	require.True(t, allowed)

	identities.authorizations["tenant-a/alice"] = authorization(
		"tenant-a", "alice", model.TenantMembershipStatusActive, model.SystemRoleUser,
	)
	allowed, err = resolver.AuthorizeProfile(context.Background(), "tenant-a", "alice", model.AgentProfileIAMAdmin)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestAuthoritativeIAMReader_IsTenantScopedAndReturnsNoSecrets(t *testing.T) {
	users := &authoritativeUsers{values: map[string]*model.User{
		"alice": {Username: "alice", Nickname: "Alice", Password: "secret", Kind: model.UserKindHuman},
		"bob":   {Username: "bob", Nickname: "Bob", Password: "secret-2", Kind: model.UserKindHuman},
	}}
	identities := &authoritativeIdentities{
		authorizations: map[string]*repo.TenantAuthorization{
			"tenant-a/alice": authorization("tenant-a", "alice", model.TenantMembershipStatusActive, model.SystemRoleUser),
		},
		memberships: map[string][]*model.TenantMembership{
			"tenant-a": {
				{TenantID: "tenant-a", Username: "alice", Status: model.TenantMembershipStatusActive, Version: 1},
				{TenantID: "tenant-a", Username: "bob", Status: model.TenantMembershipStatusDisabled, Version: 2},
			},
		},
	}
	reader, err := NewAuthoritativeIAMReader("tenant-a", users, identities)
	require.NoError(t, err)

	alice, err := reader.GetTenantUser(context.Background(), "tenant-a", "alice")
	require.NoError(t, err)
	require.Equal(t, "Alice", alice.Nickname)
	require.Empty(t, alice.Email)
	require.Empty(t, alice.Phone)
	require.True(t, alice.Active)

	items, err := reader.ListTenantUsers(context.Background(), "tenant-a", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"alice", "bob"}, []string{items[0].Username, items[1].Username})
	require.False(t, items[1].Active)
	_, err = reader.ListTenantUsers(context.Background(), "tenant-b", 20)
	require.Error(t, err)
	_, err = reader.ListTenantUsers(context.Background(), "tenant-a", 21)
	require.Error(t, err)
}

func authorization(tenantID, username, status string, roles ...string) *repo.TenantAuthorization {
	return &repo.TenantAuthorization{
		Membership: &model.TenantMembership{
			TenantID: tenantID, Username: username, Status: status, Version: 1,
		},
		Roles: roles,
	}
}

type authoritativeUsers struct{ values map[string]*model.User }

func (u *authoritativeUsers) GetUserByUsername(_ context.Context, username string) (*model.User, error) {
	user := u.values[username]
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	return user, nil
}

func (u *authoritativeUsers) GetUsersByUsernames(_ context.Context, usernames []string) ([]*model.User, error) {
	result := make([]*model.User, 0, len(usernames))
	for _, username := range usernames {
		user := u.values[username]
		if user == nil {
			return nil, fmt.Errorf("user not found")
		}
		result = append(result, user)
	}
	return result, nil
}

type authoritativeIdentities struct {
	authorizations map[string]*repo.TenantAuthorization
	memberships    map[string][]*model.TenantMembership
}

func (i *authoritativeIdentities) ResolveTenantAuthorization(
	_ context.Context,
	tenantID, username string,
) (*repo.TenantAuthorization, error) {
	value := i.authorizations[tenantID+"/"+username]
	if value == nil {
		return nil, repo.ErrTenantMembershipNotFound
	}
	return value, nil
}

func (i *authoritativeIdentities) ListTenantMemberships(
	_ context.Context,
	tenantID string,
	limit int,
) ([]*model.TenantMembership, error) {
	values := i.memberships[tenantID]
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}
