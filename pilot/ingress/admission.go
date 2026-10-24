package ingress

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

type SessionAuthority interface {
	GetSession(context.Context, string) (*model.Session, error)
	GetMembers(context.Context, string) ([]*model.SessionMember, error)
}
type UserAuthority interface {
	GetUserByUsername(context.Context, string) (*model.User, error)
}

// Admission checks the existing IM conversation boundary, for every tenant.
type ConversationAdmission struct {
	bot      string
	sessions SessionAuthority
	users    UserAuthority
	snapshot Admission
}

func NewConversationAdmission(bot string, sessions SessionAuthority, users UserAuthority, snapshot Admission) (*ConversationAdmission, error) {
	if bot == "" || sessions == nil || users == nil {
		return nil, fmt.Errorf("pilot admission dependencies are incomplete")
	}
	return &ConversationAdmission{bot: bot, sessions: sessions, users: users, snapshot: snapshot}, nil
}
func (a *ConversationAdmission) Admit(ctx context.Context, _ string, event *commonv1.ChatEvent) (Admission, error) {
	if event == nil {
		return Admission{}, ErrAdmissionMismatch
	}
	conversation, err := a.sessions.GetSession(ctx, event.SessionId)
	if err != nil {
		return Admission{}, err
	}
	if conversation.Kind != model.SessionKindAI {
		return Admission{}, nil
	}
	if conversation.TenantID == "" || conversation.OwnerUsername != event.FromUsername || conversation.Type != 1 {
		return Admission{}, ErrAdmissionDenied
	}
	members, err := a.sessions.GetMembers(ctx, event.SessionId)
	if err != nil {
		return Admission{}, err
	}
	if len(members) != 2 {
		return Admission{}, ErrAdmissionDenied
	}
	owner, bot := false, false
	for _, m := range members {
		if m == nil {
			return Admission{}, ErrAdmissionDenied
		}
		owner = owner || m.Username == conversation.OwnerUsername
		bot = bot || m.Username == a.bot
	}
	if !owner || !bot || conversation.OwnerUsername == a.bot {
		return Admission{}, ErrAdmissionDenied
	}
	user, err := a.users.GetUserByUsername(ctx, conversation.OwnerUsername)
	if err != nil {
		return Admission{}, err
	}
	if user == nil || user.Kind != model.UserKindHuman {
		return Admission{}, ErrAdmissionDenied
	}
	result := a.snapshot
	result.Trigger = true
	result.TenantID = conversation.TenantID
	result.ConversationID = conversation.SessionID
	result.ActorID = user.Username
	result.ActorUsername = user.Username
	return result, nil
}
