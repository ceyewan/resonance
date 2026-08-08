package ingress

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

type SessionAuthority interface {
	GetSession(ctx context.Context, sessionID string) (*model.Session, error)
	GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error)
}

type UserAuthority interface {
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
}

type ProfileAuthorizer interface {
	AuthorizeProfile(ctx context.Context, tenantID, actorID, profileID string) (bool, error)
}

// SingleTenantAdmission pins one Pilot instance to one tenant and one immutable
// profile snapshot, and checks both against the authoritative Session row.
type SingleTenantAdmission struct {
	tenantID string
	bot      string
	sessions SessionAuthority
	users    UserAuthority
	auth     ProfileAuthorizer
	profile  Admission
}

func NewSingleTenantAdmission(
	tenantID, botUsername string,
	sessions SessionAuthority,
	users UserAuthority,
	authorizer ProfileAuthorizer,
	profile Admission,
) (*SingleTenantAdmission, error) {
	if tenantID == "" || botUsername == "" || sessions == nil || users == nil || authorizer == nil {
		return nil, fmt.Errorf("single-tenant admission dependencies are incomplete")
	}
	profile.TenantID = tenantID
	profile.Trigger = true
	if profile.ProfileID == "" || profile.ProfileVersion <= 0 || profile.RuntimeKind == "" ||
		profile.RuntimeVersion == "" || profile.BridgeVersion == "" || profile.ModelProvider == "" ||
		profile.ModelID == "" || profile.MaxAttempts < 1 {
		return nil, fmt.Errorf("single-tenant admission profile is incomplete")
	}
	return &SingleTenantAdmission{
		tenantID: tenantID, bot: botUsername, sessions: sessions, users: users, auth: authorizer, profile: profile,
	}, nil
}

func (a *SingleTenantAdmission) Admit(ctx context.Context, tenantID string, event *commonv1.ChatEvent) (Admission, error) {
	if tenantID != a.tenantID || event == nil || event.SessionId == "" || event.FromUsername == "" {
		return Admission{}, ErrAdmissionMismatch
	}
	session, err := a.sessions.GetSession(ctx, event.SessionId)
	if err != nil {
		return Admission{}, fmt.Errorf("load authoritative session: %w", err)
	}
	if session.Kind != model.SessionKindAI {
		return Admission{Trigger: false}, nil
	}
	if session.SessionID != event.SessionId || session.TenantID != a.tenantID {
		return Admission{}, ErrAdmissionMismatch
	}
	// Another profile-specific Pilot instance owns this session. Ignore it so
	// independent queue groups can consume the same durable event topic safely.
	if session.ProfileID != a.profile.ProfileID {
		return Admission{Trigger: false}, nil
	}
	if session.ProfileVersion != a.profile.ProfileVersion {
		return Admission{}, ErrAdmissionMismatch
	}
	if session.Type != int(commonv1.SessionType_SESSION_TYPE_DIRECT) {
		return Admission{}, fmt.Errorf("AI session must be direct")
	}
	members, err := a.sessions.GetMembers(ctx, event.SessionId)
	if err != nil {
		return Admission{}, fmt.Errorf("load authoritative session members: %w", err)
	}
	if len(members) != 2 {
		return Admission{}, fmt.Errorf("AI session must contain exactly one human and one bot")
	}
	present := make(map[string]struct{}, len(members))
	for _, member := range members {
		if member == nil || member.Username == "" {
			return Admission{}, fmt.Errorf("AI session contains an invalid member")
		}
		present[member.Username] = struct{}{}
	}
	if _, ok := present[a.bot]; !ok {
		return Admission{}, fmt.Errorf("AI session is missing the configured agent bot")
	}
	if _, ok := present[event.FromUsername]; !ok || event.FromUsername == a.bot {
		return Admission{}, fmt.Errorf("event actor is not the human member of the AI session")
	}
	bot, err := a.users.GetUserByUsername(ctx, a.bot)
	if err != nil || bot.Kind != model.UserKindAgentBot {
		return Admission{}, fmt.Errorf("configured agent bot identity is invalid")
	}
	actor, err := a.users.GetUserByUsername(ctx, event.FromUsername)
	if err != nil {
		return Admission{}, fmt.Errorf("load authoritative actor: %w", err)
	}
	if actor.Kind != model.UserKindHuman {
		return Admission{}, fmt.Errorf("AI session actor must be a human account")
	}
	allowed, err := a.auth.AuthorizeProfile(ctx, a.tenantID, actor.Username, a.profile.ProfileID)
	if err != nil {
		return Admission{}, fmt.Errorf("authorize agent profile: %w", err)
	}
	if !allowed {
		return Admission{}, ErrAdmissionDenied
	}

	admission := a.profile
	admission.ConversationID = event.SessionId
	admission.ActorID = actor.Username
	admission.ActorUsername = actor.Username
	return admission, nil
}
