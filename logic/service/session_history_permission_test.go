package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testSessionRepo struct {
	getUserSessionFn func(ctx context.Context, username, sessionID string) (*model.SessionMember, error)
}

func (r *testSessionRepo) CreateSession(ctx context.Context, session *model.Session) error {
	return nil
}
func (r *testSessionRepo) GetSession(ctx context.Context, sessionID string) (*model.Session, error) {
	return nil, nil
}
func (r *testSessionRepo) GetUserSession(ctx context.Context, username, sessionID string) (*model.SessionMember, error) {
	if r.getUserSessionFn != nil {
		return r.getUserSessionFn(ctx, username, sessionID)
	}
	return nil, nil
}
func (r *testSessionRepo) GetUserSessionList(ctx context.Context, username string) ([]*model.Session, error) {
	return nil, nil
}
func (r *testSessionRepo) GetUserSessionsBatch(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error) {
	return nil, nil
}
func (r *testSessionRepo) AddMember(ctx context.Context, member *model.SessionMember) error {
	return nil
}
func (r *testSessionRepo) GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error) {
	return nil, nil
}
func (r *testSessionRepo) UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error {
	return nil
}
func (r *testSessionRepo) GetContactList(ctx context.Context, username string) ([]*model.User, error) {
	return nil, nil
}
func (r *testSessionRepo) UpdateLastReadSeq(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
	return nil
}
func (r *testSessionRepo) Close() error { return nil }

type testMessageRepo struct {
	historyCalled bool
}

func (r *testMessageRepo) SaveMessage(ctx context.Context, msg *model.MessageContent) error {
	return nil
}
func (r *testMessageRepo) SaveInbox(ctx context.Context, inboxes []*model.Inbox) error { return nil }
func (r *testMessageRepo) GetHistoryMessages(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error) {
	r.historyCalled = true
	return nil, nil
}
func (r *testMessageRepo) GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error) {
	return nil, nil
}
func (r *testMessageRepo) GetLastMessagesBatch(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error) {
	return nil, nil
}
func (r *testMessageRepo) GetUnreadMessages(ctx context.Context, username string, limit int) ([]*model.Inbox, error) {
	return nil, nil
}
func (r *testMessageRepo) GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*repo.InboxDeltaItem, error) {
	return nil, nil
}
func (r *testMessageRepo) SaveMessageWithOutbox(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) error {
	return nil
}
func (r *testMessageRepo) UpdateOutboxStatus(ctx context.Context, id int64, status int) error {
	return nil
}
func (r *testMessageRepo) UpdateOutboxRetry(ctx context.Context, id int64, nextRetry time.Time, count int) error {
	return nil
}
func (r *testMessageRepo) GetPendingOutboxMessages(ctx context.Context, limit int) ([]*model.MessageOutbox, error) {
	return nil, nil
}
func (r *testMessageRepo) Close() error { return nil }

type testUserRepo struct{}

func (r *testUserRepo) CreateUser(ctx context.Context, user *model.User) error { return nil }
func (r *testUserRepo) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return nil, nil
}
func (r *testUserRepo) GetUsersByUsernames(ctx context.Context, usernames []string) ([]*model.User, error) {
	return nil, nil
}
func (r *testUserRepo) SearchUsers(ctx context.Context, query string) ([]*model.User, error) {
	return nil, nil
}
func (r *testUserRepo) UpdateUser(ctx context.Context, user *model.User) error { return nil }
func (r *testUserRepo) Close() error                                           { return nil }

func TestSessionService_GetHistoryMessages_DeniedForNonMember(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionFn: func(ctx context.Context, username, sessionID string) (*model.SessionMember, error) {
			return nil, fmt.Errorf("user session not found: username=%s, session_id=%s", username, sessionID)
		},
	}
	messageRepo := &testMessageRepo{}
	logger := clog.Discard()

	svc := NewSessionService(sessionRepo, messageRepo, &testUserRepo{}, nil, nil, nil, nil, logger)

	_, err := svc.GetHistoryMessages(context.Background(), &logicv1.GetHistoryMessagesRequest{
		Username:  "mallory",
		SessionId: "s_123",
		Limit:     20,
	})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, messageRepo.historyCalled, "越权时不应触发历史消息查询")
}
