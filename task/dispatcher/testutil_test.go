package dispatcher

import (
	"context"
	"errors"
	"time"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/task/pusher"
)

type testMessageRepo struct {
	saveInboxBatchFn func(ctx context.Context, inboxes []*model.Inbox) error
	savedInboxes     []*model.Inbox
}

func (r *testMessageRepo) SaveMessageContent(ctx context.Context, msg *model.MessageContent) error {
	return nil
}

func (r *testMessageRepo) SaveInboxBatch(ctx context.Context, inboxes []*model.Inbox) error {
	r.savedInboxes = inboxes
	if r.saveInboxBatchFn != nil {
		return r.saveInboxBatchFn(ctx, inboxes)
	}
	return nil
}

func (r *testMessageRepo) GetHistoryMessages(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error) {
	return nil, nil
}

func (r *testMessageRepo) GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error) {
	return nil, nil
}

func (r *testMessageRepo) GetLastMessagesBatch(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error) {
	return nil, nil
}

func (r *testMessageRepo) GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
	return nil, nil
}

func (r *testMessageRepo) GetUnreadMessageCount(ctx context.Context, username, sessionID string) (int64, error) {
	return 0, nil
}

func (r *testMessageRepo) GetMessageByEventID(ctx context.Context, eventID int64) (*model.MessageContent, error) {
	return nil, nil
}

func (r *testMessageRepo) GetMessageByIdempotencyKey(ctx context.Context, sessionID, senderUsername, clientMsgID string) (*model.MessageContent, error) {
	return nil, repo.ErrMessageNotFound
}

func (r *testMessageRepo) RecallMessageWithOutbox(ctx context.Context, eventID int64, recalledAt time.Time, outbox *model.MessageOutbox) error {
	return nil
}

func (r *testMessageRepo) EditMessageWithOutbox(ctx context.Context, eventID int64, newContent string, editedAt time.Time, outbox *model.MessageOutbox) error {
	return nil
}

func (r *testMessageRepo) MarkMessageRecalled(ctx context.Context, eventID int64, at time.Time) error {
	return nil
}

func (r *testMessageRepo) UpdateMessageContent(ctx context.Context, eventID int64, newContent string, at time.Time) error {
	return nil
}

func (r *testMessageRepo) SaveMessageWithOutbox(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) (*repo.MessageSaveResult, error) {
	return &repo.MessageSaveResult{Message: msg, Outbox: outbox, Created: true}, nil
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

type testRouterRepo struct {
	batchGetUsersGatewayFn func(ctx context.Context, usernames []string) ([]*model.Router, error)
}

func (r *testRouterRepo) SetUserGateway(ctx context.Context, router *model.Router) error { return nil }
func (r *testRouterRepo) GetUserGateway(ctx context.Context, username string) (*model.Router, error) {
	return nil, errors.New("not found")
}
func (r *testRouterRepo) DeleteUserGateway(ctx context.Context, username string) error { return nil }
func (r *testRouterRepo) BatchSetUserGateway(ctx context.Context, routers []*model.Router) error {
	return nil
}
func (r *testRouterRepo) BatchDeleteUserGateway(ctx context.Context, usernames []string) error {
	return nil
}
func (r *testRouterRepo) BatchGetUsersGateway(ctx context.Context, usernames []string) ([]*model.Router, error) {
	if r.batchGetUsersGatewayFn != nil {
		return r.batchGetUsersGatewayFn(ctx, usernames)
	}
	return nil, nil
}
func (r *testRouterRepo) Close() error { return nil }

type testPusherManager struct {
	getFn func(gatewayID string) (pusher.Client, error)
}

func (m *testPusherManager) Start() error { return nil }

func (m *testPusherManager) GetClient(gatewayID string) (pusher.Client, error) {
	if m.getFn != nil {
		return m.getFn(gatewayID)
	}
	return nil, errors.New("not found")
}

func (m *testPusherManager) Close() {}
