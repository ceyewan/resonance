package ingress

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

func TestSingleTenantAdmission_RequiresExactHumanBotAISession(t *testing.T) {
	sessions := &fakeSessionAuthority{
		session: &model.Session{SessionID: "ai-1", Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI, TenantID: "tenant-a", ProfileID: "user-assistant", ProfileVersion: 1},
		members: []*model.SessionMember{{SessionID: "ai-1", Username: "user-1"}, {SessionID: "ai-1", Username: "resonance-agent"}},
	}
	users := &fakeUserAuthority{users: map[string]*model.User{
		"user-1":          {Username: "user-1", Kind: model.UserKindHuman},
		"resonance-agent": {Username: "resonance-agent", Kind: model.UserKindAgentBot},
	}}
	authorizer := &fakeProfileAuthorizer{allowed: true}
	admission, err := NewSingleTenantAdmission("tenant-a", "resonance-agent", sessions, users, authorizer, testAdmission())
	require.NoError(t, err)

	result, err := admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.NoError(t, err)
	require.True(t, result.Trigger)
	require.Equal(t, "user-1", result.ActorID)
	require.Equal(t, "ai-1", result.ConversationID)
	require.Equal(t, "tenant-a", result.TenantID)
	require.Equal(t, "user-1", authorizer.actorID)
	require.Equal(t, model.AgentProfileUserAssistant, authorizer.profileID)

	sessions.session.Kind = model.SessionKindStandard
	ignored, err := admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.NoError(t, err)
	require.False(t, ignored.Trigger)
}

func TestSingleTenantAdmission_FailsClosedOnMembershipAndBotKindAnomalies(t *testing.T) {
	sessions := &fakeSessionAuthority{
		session: &model.Session{SessionID: "ai-1", Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI, TenantID: "tenant-a", ProfileID: "user-assistant", ProfileVersion: 1},
		members: []*model.SessionMember{{Username: "user-1"}, {Username: "intruder"}, {Username: "resonance-agent"}},
	}
	users := &fakeUserAuthority{users: map[string]*model.User{
		"user-1":          {Username: "user-1", Kind: model.UserKindHuman},
		"resonance-agent": {Username: "resonance-agent", Kind: model.UserKindAgentBot},
	}}
	admission, err := NewSingleTenantAdmission("tenant-a", "resonance-agent", sessions, users, &fakeProfileAuthorizer{allowed: true}, testAdmission())
	require.NoError(t, err)
	_, err = admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.Error(t, err)

	sessions.members = []*model.SessionMember{{Username: "user-1"}, {Username: "resonance-agent"}}
	users.users["resonance-agent"].Kind = model.UserKindHuman
	_, err = admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.Error(t, err)

	_, err = admission.Admit(context.Background(), "other-tenant", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.ErrorIs(t, err, ErrAdmissionMismatch)
}

func TestSingleTenantAdmission_PinsTenantProfileAndVersion(t *testing.T) {
	sessions := &fakeSessionAuthority{
		session: &model.Session{SessionID: "ai-1", Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI, TenantID: "tenant-a", ProfileID: "iam-admin", ProfileVersion: 1},
		members: []*model.SessionMember{{Username: "user-1"}, {Username: "resonance-agent"}},
	}
	users := &fakeUserAuthority{users: map[string]*model.User{
		"user-1":          {Username: "user-1", Kind: model.UserKindHuman},
		"resonance-agent": {Username: "resonance-agent", Kind: model.UserKindAgentBot},
	}}
	admission, err := NewSingleTenantAdmission("tenant-a", "resonance-agent", sessions, users, &fakeProfileAuthorizer{allowed: true}, testAdmission())
	require.NoError(t, err)

	ignored, err := admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.NoError(t, err)
	require.False(t, ignored.Trigger, "a different profile instance must ignore the session")

	sessions.session.ProfileID = "user-assistant"
	sessions.session.ProfileVersion = 2
	_, err = admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.ErrorIs(t, err, ErrAdmissionMismatch)

	sessions.session.ProfileVersion = 1
	sessions.session.TenantID = "tenant-b"
	_, err = admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.ErrorIs(t, err, ErrAdmissionMismatch)
}

func TestSingleTenantAdmission_RejectsRevokedProfileBeforeEnqueue(t *testing.T) {
	sessions := &fakeSessionAuthority{
		session: &model.Session{SessionID: "ai-1", Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI, TenantID: "tenant-a", ProfileID: model.AgentProfileIAMAdmin, ProfileVersion: 1},
		members: []*model.SessionMember{{Username: "user-1"}, {Username: "resonance-agent"}},
	}
	users := &fakeUserAuthority{users: map[string]*model.User{
		"user-1":          {Username: "user-1", Kind: model.UserKindHuman},
		"resonance-agent": {Username: "resonance-agent", Kind: model.UserKindAgentBot},
	}}
	profile := testAdmission()
	profile.ProfileID = model.AgentProfileIAMAdmin
	admission, err := NewSingleTenantAdmission(
		"tenant-a", "resonance-agent", sessions, users, &fakeProfileAuthorizer{allowed: false}, profile,
	)
	require.NoError(t, err)

	_, err = admission.Admit(context.Background(), "tenant-a", &commonv1.ChatEvent{SessionId: "ai-1", FromUsername: "user-1"})
	require.ErrorIs(t, err, ErrAdmissionDenied)
}

type fakeSessionAuthority struct {
	session *model.Session
	members []*model.SessionMember
	err     error
}

func (a *fakeSessionAuthority) GetSession(context.Context, string) (*model.Session, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.session, nil
}
func (a *fakeSessionAuthority) GetMembers(context.Context, string) ([]*model.SessionMember, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.members, nil
}

type fakeUserAuthority struct {
	users map[string]*model.User
}

type fakeProfileAuthorizer struct {
	allowed   bool
	err       error
	actorID   string
	profileID string
}

func (a *fakeProfileAuthorizer) AuthorizeProfile(
	_ context.Context,
	_, actorID, profileID string,
) (bool, error) {
	a.actorID = actorID
	a.profileID = profileID
	return a.allowed, a.err
}

func (a *fakeUserAuthority) GetUserByUsername(_ context.Context, username string) (*model.User, error) {
	user, ok := a.users[username]
	if !ok {
		return nil, errors.New("not found")
	}
	return user, nil
}
