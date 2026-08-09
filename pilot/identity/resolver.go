package identity

import (
	"context"
	"fmt"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

type UserReader interface {
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
}

type StaticProfileResolver struct {
	tenantID string
	profile  pilotruntime.ProfileSnapshot
}

func NewStaticProfileResolver(tenantID string, profile pilotruntime.ProfileSnapshot) (*StaticProfileResolver, error) {
	if tenantID == "" || profile.ID == "" || profile.Version < 1 || profile.Provider == "" || profile.Model == "" || profile.SystemPrompt == "" {
		return nil, fmt.Errorf("static agent profile is incomplete")
	}
	return &StaticProfileResolver{tenantID: tenantID, profile: profile}, nil
}

func (r *StaticProfileResolver) ResolveProfile(
	_ context.Context,
	tenantID, profileID string,
	version int64,
) (pilotruntime.ProfileSnapshot, error) {
	if tenantID != r.tenantID || profileID != r.profile.ID || version != r.profile.Version {
		return pilotruntime.ProfileSnapshot{}, fmt.Errorf("agent profile snapshot is not configured")
	}
	return r.profile, nil
}

// SingleTenantPrincipalResolver is deliberately narrow while the core IAM
// schema has no tenant_id column. The configured tenant is authoritative and
// actorID must equal the immutable username used by current repositories.
type SingleTenantPrincipalResolver struct {
	tenantID string
	users    UserReader
}

func NewSingleTenantPrincipalResolver(tenantID string, users UserReader) (*SingleTenantPrincipalResolver, error) {
	if tenantID == "" || users == nil {
		return nil, fmt.Errorf("single-tenant principal resolver is incomplete")
	}
	return &SingleTenantPrincipalResolver{tenantID: tenantID, users: users}, nil
}

func (r *SingleTenantPrincipalResolver) ResolvePrincipal(
	ctx context.Context,
	tenantID, actorID string,
) (pilotruntime.ActorPrincipal, error) {
	if tenantID != r.tenantID || actorID == "" {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("actor principal is outside the configured tenant")
	}
	user, err := r.users.GetUserByUsername(ctx, actorID)
	if err != nil {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("load actor principal: %w", err)
	}
	if user.Username != actorID || user.Kind != model.UserKindHuman {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("actor principal is not an active human identity")
	}
	return pilotruntime.ActorPrincipal{
		TenantID: tenantID,
		ActorID:  user.Username,
		Username: user.Username,
	}, nil
}
