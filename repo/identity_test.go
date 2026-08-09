package repo

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestIdentityRepo_CreateIdentityAndResolveTenantScoped(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	user := &model.User{Username: "identity-alice", Password: "hashed", Kind: model.UserKindHuman}
	membership := &model.TenantMembership{
		TenantID: "tenant-a",
		Username: user.Username,
		Status:   model.TenantMembershipStatusActive,
		Version:  1,
	}
	require.NoError(t, identityRepo.CreateIdentity(ctx, user, membership, []string{
		model.SystemRoleIAMAdmin,
		model.SystemRoleUser,
		model.SystemRoleUser,
	}))

	authorization, err := identityRepo.ResolveTenantAuthorization(ctx, "tenant-a", user.Username)
	require.NoError(t, err)
	require.Equal(t, model.TenantMembershipStatusActive, authorization.Membership.Status)
	require.Equal(t, int64(1), authorization.Membership.Version)
	require.Equal(t, []string{model.SystemRoleIAMAdmin, model.SystemRoleUser}, authorization.Roles)

	_, err = identityRepo.ResolveTenantAuthorization(ctx, "tenant-b", user.Username)
	require.ErrorIs(t, err, ErrTenantMembershipNotFound)
	_, err = identityRepo.ResolveTenantAuthorization(ctx, "", user.Username)
	require.Error(t, err)
}

func TestIdentityRepo_SameUserRolesAreIsolatedAcrossTenants(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	userRepo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	ctx := context.Background()
	username := "cross-tenant-user"
	require.NoError(t, userRepo.CreateUser(ctx, &model.User{Username: username, Password: "hashed"}))
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		require.NoError(t, identityRepo.CreateTenantMembership(ctx, &model.TenantMembership{
			TenantID: tenantID,
			Username: username,
			Status:   model.TenantMembershipStatusActive,
			Version:  1,
		}))
	}
	require.NoError(t, identityRepo.CreateSystemRoleBinding(ctx, &model.SystemRoleBinding{
		TenantID: "tenant-a", Username: username, Role: model.SystemRoleUser,
	}))
	require.NoError(t, identityRepo.CreateSystemRoleBinding(ctx, &model.SystemRoleBinding{
		TenantID: "tenant-b", Username: username, Role: model.SystemRoleIAMAdmin,
	}))

	tenantA, err := identityRepo.ResolveTenantAuthorization(ctx, "tenant-a", username)
	require.NoError(t, err)
	require.Equal(t, []string{model.SystemRoleUser}, tenantA.Roles)
	tenantB, err := identityRepo.ResolveTenantAuthorization(ctx, "tenant-b", username)
	require.NoError(t, err)
	require.Equal(t, []string{model.SystemRoleIAMAdmin}, tenantB.Roles)
}

func TestIdentityRepo_ListTenantMembershipsNeverCrossesTenant(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	for _, username := range []string{"list-alice", "list-bob"} {
		require.NoError(t, identityRepo.CreateIdentity(ctx,
			&model.User{Username: username, Password: "hashed", Kind: model.UserKindHuman},
			&model.TenantMembership{
				TenantID: "tenant-list-a", Username: username,
				Status: model.TenantMembershipStatusActive, Version: 1,
			},
			[]string{model.SystemRoleUser},
		))
	}
	require.NoError(t, identityRepo.CreateTenantMembership(ctx, &model.TenantMembership{
		TenantID: "tenant-list-b", Username: "list-alice",
		Status: model.TenantMembershipStatusDisabled, Version: 1,
	}))

	memberships, err := identityRepo.ListTenantMemberships(ctx, "tenant-list-a", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"list-alice", "list-bob"}, []string{
		memberships[0].Username, memberships[1].Username,
	})
	require.Len(t, memberships, 2)
	other, err := identityRepo.ListTenantMemberships(ctx, "tenant-list-b", 20)
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, model.TenantMembershipStatusDisabled, other[0].Status)
	_, err = identityRepo.ListTenantMemberships(ctx, "tenant-list-a", 0)
	require.Error(t, err)
}

func TestIdentityRepo_ConcurrentRoleBindingIsIdempotent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	username := "concurrent-role-user"
	require.NoError(t, identityRepo.CreateIdentity(ctx,
		&model.User{Username: username, Password: "hashed"},
		&model.TenantMembership{TenantID: "tenant-a", Username: username, Status: model.TenantMembershipStatusActive, Version: 1},
		[]string{model.SystemRoleUser},
	))

	const workers = 16
	start := make(chan struct{})
	errorsCh := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			errorsCh <- identityRepo.CreateSystemRoleBinding(ctx, &model.SystemRoleBinding{
				TenantID: "tenant-a", Username: username, Role: model.SystemRoleIAMAdmin,
			})
		})
	}
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}

	bindings, err := identityRepo.ListSystemRoleBindings(ctx, "tenant-a", username)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	membership, err := identityRepo.GetTenantMembership(ctx, "tenant-a", username)
	require.NoError(t, err)
	require.Equal(t, int64(2), membership.Version, "并发幂等绑定只能使成员版本前进一次")
	require.NoError(t, identityRepo.DeleteSystemRoleBinding(ctx, "tenant-a", username, model.SystemRoleIAMAdmin))
	membership, err = identityRepo.GetTenantMembership(ctx, "tenant-a", username)
	require.NoError(t, err)
	require.Equal(t, int64(3), membership.Version)
}

func TestIdentityRepo_ConcurrentMembershipUpdateUsesVersionFence(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	username := "membership-fence-user"
	require.NoError(t, identityRepo.CreateIdentity(ctx,
		&model.User{Username: username, Password: "hashed"},
		&model.TenantMembership{TenantID: "tenant-a", Username: username, Status: model.TenantMembershipStatusActive, Version: 1},
		[]string{model.SystemRoleUser},
	))

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, status := range []string{model.TenantMembershipStatusActive, model.TenantMembershipStatusDisabled} {
		wg.Go(func() {
			<-start
			_, updateErr := identityRepo.UpdateTenantMembershipStatus(ctx, "tenant-a", username, status, 1)
			errorsCh <- updateErr
		})
	}
	close(start)
	wg.Wait()
	close(errorsCh)

	var successes, conflicts int
	for err := range errorsCh {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrIdentityVersionConflict):
			conflicts++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	membership, err := identityRepo.GetTenantMembership(ctx, "tenant-a", username)
	require.NoError(t, err)
	require.Equal(t, int64(2), membership.Version)
}

func TestIdentityRepo_FailsClosedForUnknownRoleAndMissingMembership(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	err = identityRepo.CreateSystemRoleBinding(ctx, &model.SystemRoleBinding{
		TenantID: "tenant-a", Username: "missing", Role: model.SystemRoleUser,
	})
	require.ErrorIs(t, err, ErrTenantMembershipNotFound)
	err = identityRepo.CreateSystemRoleBinding(ctx, &model.SystemRoleBinding{
		TenantID: "tenant-a", Username: "missing", Role: "session-admin",
	})
	require.ErrorIs(t, err, ErrUnknownSystemRole)
}
