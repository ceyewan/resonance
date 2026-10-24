// Package identity resolves only the IM human identity. There are no Agent roles or scopes.
package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

var ErrPrincipalUnauthorized = errors.New("pilot requires a human account")

type UserReader interface {
	GetUserByUsername(context.Context, string) (*model.User, error)
}

type StaticProfileResolver struct{ profile pilotruntime.ProfileSnapshot }

func NewStaticProfileResolver(profile pilotruntime.ProfileSnapshot) (*StaticProfileResolver, error) {
	if profile.ID == "" || profile.Version < 1 || profile.Provider == "" || profile.Model == "" || profile.SystemPrompt == "" {
		return nil, fmt.Errorf("assistant model configuration is incomplete")
	}
	return &StaticProfileResolver{profile: profile}, nil
}
func (r *StaticProfileResolver) ResolveProfile(_ context.Context, tenantID, id string, version int64) (pilotruntime.ProfileSnapshot, error) {
	if tenantID == "" || id != r.profile.ID || version != r.profile.Version {
		return pilotruntime.ProfileSnapshot{}, fmt.Errorf("assistant configuration has changed")
	}
	return r.profile, nil
}

type PrincipalResolver struct{ users UserReader }

func NewPrincipalResolver(users UserReader) (*PrincipalResolver, error) {
	if users == nil {
		return nil, fmt.Errorf("user reader is required")
	}
	return &PrincipalResolver{users: users}, nil
}
func (r *PrincipalResolver) ResolvePrincipal(ctx context.Context, tenantID, actorID string) (pilotruntime.ActorPrincipal, error) {
	if tenantID == "" || actorID == "" {
		return pilotruntime.ActorPrincipal{}, ErrPrincipalUnauthorized
	}
	user, err := r.users.GetUserByUsername(ctx, actorID)
	if err != nil {
		return pilotruntime.ActorPrincipal{}, err
	}
	if user == nil || user.Username != actorID || user.Kind != model.UserKindHuman {
		return pilotruntime.ActorPrincipal{}, ErrPrincipalUnauthorized
	}
	return pilotruntime.ActorPrincipal{TenantID: tenantID, ActorID: actorID, Username: actorID}, nil
}
