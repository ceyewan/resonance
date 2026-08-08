package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/toolbroker"
	sharediam "github.com/ceyewan/resonance/pkg/iam"
	"github.com/ceyewan/resonance/repo"
)

var ErrPrincipalUnauthorized = errors.New("agent principal is not currently authorized")

type AuthorizationReader interface {
	ResolveTenantAuthorization(ctx context.Context, tenantID, username string) (*repo.TenantAuthorization, error)
}

type TenantMembershipReader interface {
	AuthorizationReader
	ListTenantMemberships(ctx context.Context, tenantID string, limit int) ([]*model.TenantMembership, error)
}

type IAMUserReader interface {
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	GetUsersByUsernames(ctx context.Context, usernames []string) ([]*model.User, error)
}

// AuthoritativePrincipalResolver reloads membership and roles for every Tool
// Broker request. The configured tenant is a worker-pool boundary in addition
// to the tenant carried by the Capability.
type AuthoritativePrincipalResolver struct {
	tenantID   string
	users      UserReader
	identities AuthorizationReader
}

func NewAuthoritativePrincipalResolver(
	tenantID string,
	users UserReader,
	identities AuthorizationReader,
) (*AuthoritativePrincipalResolver, error) {
	if tenantID == "" || users == nil || identities == nil {
		return nil, fmt.Errorf("authoritative principal resolver is incomplete")
	}
	return &AuthoritativePrincipalResolver{tenantID: tenantID, users: users, identities: identities}, nil
}

func (r *AuthoritativePrincipalResolver) ResolvePrincipal(
	ctx context.Context,
	tenantID, actorID string,
) (pilotruntime.ActorPrincipal, error) {
	if tenantID != r.tenantID || actorID == "" {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("actor principal is outside the configured tenant")
	}
	authorization, err := r.identities.ResolveTenantAuthorization(ctx, tenantID, actorID)
	if err != nil {
		if errors.Is(err, repo.ErrTenantMembershipNotFound) {
			return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: tenant membership is absent", ErrPrincipalUnauthorized)
		}
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("load actor authorization: %w", err)
	}
	if err := validateAuthorization(authorization, tenantID, actorID, true); err != nil {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: %v", ErrPrincipalUnauthorized, err)
	}
	user, err := r.users.GetUserByUsername(ctx, actorID)
	if err != nil {
		if errors.Is(err, repo.ErrUserNotFound) {
			return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: human identity is absent", ErrPrincipalUnauthorized)
		}
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("load actor principal: %w", err)
	}
	if user == nil || user.Username != actorID || user.Kind != model.UserKindHuman {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: actor is not an active human identity", ErrPrincipalUnauthorized)
	}
	roles, err := sharediam.CanonicalSystemRoles(authorization.Roles)
	if err != nil || len(roles) == 0 {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: actor has an invalid system role set", ErrPrincipalUnauthorized)
	}
	scopes, err := sharediam.ScopesForSystemRoles(roles)
	if err != nil || len(scopes) == 0 {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: actor has an invalid scope set", ErrPrincipalUnauthorized)
	}
	return pilotruntime.ActorPrincipal{
		TenantID: tenantID,
		ActorID:  actorID,
		Username: user.Username,
		Roles:    roles,
		Scopes:   scopes,
	}, nil
}

// AuthorizeProfile is the ingress-side preflight. The Coordinator repeats the
// same decision after claiming the durable Run so queued work cannot retain a
// revoked administrator capability.
func (r *AuthoritativePrincipalResolver) AuthorizeProfile(
	ctx context.Context,
	tenantID, actorID, profileID string,
) (bool, error) {
	principal, err := r.ResolvePrincipal(ctx, tenantID, actorID)
	if err != nil {
		if errors.Is(err, ErrPrincipalUnauthorized) {
			return false, nil
		}
		return false, err
	}
	if err := sharediam.AuthorizeAgentProfile(profileID, principal.Roles, principal.Scopes); err != nil {
		if errors.Is(err, sharediam.ErrAgentProfileUnauthorized) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// AuthoritativeIAMReader exposes only the tenant-scoped, password-free read
// model accepted by Tool Broker.
type AuthoritativeIAMReader struct {
	tenantID   string
	users      IAMUserReader
	identities TenantMembershipReader
}

func NewAuthoritativeIAMReader(
	tenantID string,
	users IAMUserReader,
	identities TenantMembershipReader,
) (*AuthoritativeIAMReader, error) {
	if tenantID == "" || users == nil || identities == nil {
		return nil, fmt.Errorf("authoritative IAM reader is incomplete")
	}
	return &AuthoritativeIAMReader{tenantID: tenantID, users: users, identities: identities}, nil
}

func (r *AuthoritativeIAMReader) GetTenantUser(
	ctx context.Context,
	tenantID, username string,
) (*toolbroker.IAMUser, error) {
	if tenantID != r.tenantID || username == "" {
		return nil, fmt.Errorf("IAM user is outside the configured tenant")
	}
	authorization, err := r.identities.ResolveTenantAuthorization(ctx, tenantID, username)
	if err != nil {
		return nil, fmt.Errorf("load IAM user authorization: %w", err)
	}
	if err := validateAuthorization(authorization, tenantID, username, false); err != nil {
		return nil, err
	}
	user, err := r.users.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("load IAM user: %w", err)
	}
	return iamUser(tenantID, user, authorization.Membership)
}

func (r *AuthoritativeIAMReader) ListTenantUsers(
	ctx context.Context,
	tenantID string,
	limit int,
) ([]toolbroker.IAMUser, error) {
	if tenantID != r.tenantID || limit < 1 || limit > 20 {
		return nil, fmt.Errorf("IAM user list request is outside the configured tenant or limit")
	}
	memberships, err := r.identities.ListTenantMemberships(ctx, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list IAM memberships: %w", err)
	}
	usernames := make([]string, 0, len(memberships))
	for _, membership := range memberships {
		if err := validateMembership(membership, tenantID, false); err != nil {
			return nil, err
		}
		usernames = append(usernames, membership.Username)
	}
	users, err := r.users.GetUsersByUsernames(ctx, usernames)
	if err != nil {
		return nil, fmt.Errorf("load IAM users: %w", err)
	}
	byUsername := make(map[string]*model.User, len(users))
	for _, user := range users {
		if user == nil || user.Username == "" {
			return nil, fmt.Errorf("IAM user directory returned an invalid record")
		}
		if _, duplicate := byUsername[user.Username]; duplicate {
			return nil, fmt.Errorf("IAM user directory returned a duplicate record")
		}
		byUsername[user.Username] = user
	}
	result := make([]toolbroker.IAMUser, 0, len(memberships))
	for _, membership := range memberships {
		item, err := iamUser(tenantID, byUsername[membership.Username], membership)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, nil
}

func validateAuthorization(
	authorization *repo.TenantAuthorization,
	tenantID, username string,
	requireActive bool,
) error {
	if authorization == nil {
		return fmt.Errorf("tenant authorization is missing")
	}
	if err := validateMembership(authorization.Membership, tenantID, requireActive); err != nil {
		return err
	}
	if authorization.Membership.Username != username {
		return fmt.Errorf("tenant authorization username does not match")
	}
	return nil
}

func validateMembership(membership *model.TenantMembership, tenantID string, requireActive bool) error {
	if membership == nil || membership.TenantID != tenantID || membership.Username == "" || membership.Version < 1 {
		return fmt.Errorf("tenant membership is invalid")
	}
	if membership.Status != model.TenantMembershipStatusActive &&
		membership.Status != model.TenantMembershipStatusDisabled {
		return fmt.Errorf("tenant membership status is invalid")
	}
	if requireActive && membership.Status != model.TenantMembershipStatusActive {
		return fmt.Errorf("tenant membership is not active")
	}
	return nil
}

func iamUser(
	tenantID string,
	user *model.User,
	membership *model.TenantMembership,
) (*toolbroker.IAMUser, error) {
	if user == nil || membership == nil || user.Username != membership.Username || user.Kind != model.UserKindHuman {
		return nil, fmt.Errorf("IAM user record is inconsistent or not human")
	}
	return &toolbroker.IAMUser{
		TenantID: tenantID,
		Username: user.Username,
		Nickname: user.Nickname,
		Active:   membership.Status == model.TenantMembershipStatusActive,
	}, nil
}
