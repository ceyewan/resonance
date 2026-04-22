package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"google.golang.org/grpc/metadata"

	"github.com/ceyewan/resonance/model"
)

func newTestIncomingContext(username string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(metadataUsernameKey, username))
}

type testSessionRepo struct {
	getSessionFn                func(ctx context.Context, sessionID string) (*model.Session, error)
	getUserSessionFn            func(ctx context.Context, username, sessionID string) (*model.SessionMember, error)
	getUserSessBatch            func(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error)
	getMembersFn                func(ctx context.Context, sessionID string) ([]*model.SessionMember, error)
	getContactListFn            func(ctx context.Context, username string) ([]*model.User, error)
	updateLastReadFn            func(ctx context.Context, sessionID, username string, lastReadSeq int64) error
	advanceLastReadWithOutboxFn func(ctx context.Context, sessionID, username string, lastReadSeq int64, outbox *model.MessageOutbox) (bool, error)
	createSessionFn             func(ctx context.Context, session *model.Session) error
	addMemberFn                 func(ctx context.Context, member *model.SessionMember) error
	getUserSessionLFn           func(ctx context.Context, username string) ([]*model.Session, error)

	createdSession *model.Session
	addedMembers   []*model.SessionMember
}

func (r *testSessionRepo) CreateSession(ctx context.Context, session *model.Session) error {
	r.createdSession = session
	if r.createSessionFn != nil {
		return r.createSessionFn(ctx, session)
	}
	return nil
}

func (r *testSessionRepo) GetSession(ctx context.Context, sessionID string) (*model.Session, error) {
	if r.getSessionFn != nil {
		return r.getSessionFn(ctx, sessionID)
	}
	return &model.Session{SessionID: sessionID}, nil
}

func (r *testSessionRepo) GetUserSession(ctx context.Context, username, sessionID string) (*model.SessionMember, error) {
	if r.getUserSessionFn != nil {
		return r.getUserSessionFn(ctx, username, sessionID)
	}
	return nil, nil
}

func (r *testSessionRepo) GetUserSessionList(ctx context.Context, username string) ([]*model.Session, error) {
	if r.getUserSessionLFn != nil {
		return r.getUserSessionLFn(ctx, username)
	}
	return nil, nil
}

func (r *testSessionRepo) GetUserSessionsBatch(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error) {
	if r.getUserSessBatch != nil {
		return r.getUserSessBatch(ctx, username, sessionIDs)
	}
	return nil, nil
}

func (r *testSessionRepo) AddMember(ctx context.Context, member *model.SessionMember) error {
	r.addedMembers = append(r.addedMembers, member)
	if r.addMemberFn != nil {
		return r.addMemberFn(ctx, member)
	}
	return nil
}

func (r *testSessionRepo) GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error) {
	if r.getMembersFn != nil {
		return r.getMembersFn(ctx, sessionID)
	}
	return nil, nil
}

func (r *testSessionRepo) UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error {
	return nil
}

func (r *testSessionRepo) GetContactList(ctx context.Context, username string) ([]*model.User, error) {
	if r.getContactListFn != nil {
		return r.getContactListFn(ctx, username)
	}
	return nil, nil
}

func (r *testSessionRepo) UpdateLastReadSeq(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
	if r.updateLastReadFn != nil {
		return r.updateLastReadFn(ctx, sessionID, username, lastReadSeq)
	}
	return nil
}

func (r *testSessionRepo) AdvanceLastReadSeqWithOutbox(ctx context.Context, sessionID, username string, lastReadSeq int64, outbox *model.MessageOutbox) (bool, error) {
	if r.advanceLastReadWithOutboxFn != nil {
		return r.advanceLastReadWithOutboxFn(ctx, sessionID, username, lastReadSeq, outbox)
	}
	if r.updateLastReadFn != nil {
		return true, r.updateLastReadFn(ctx, sessionID, username, lastReadSeq)
	}
	if outbox != nil && outbox.ID == 0 {
		outbox.ID = 3
	}
	return true, nil
}

func (r *testSessionRepo) Close() error { return nil }

type testMessageRepo struct {
	historyCalled bool

	saveMessageContentFn      func(ctx context.Context, msg *model.MessageContent) error
	saveInboxBatchFn          func(ctx context.Context, inboxes []*model.Inbox) error
	getHistoryMessagesFn      func(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error)
	getLastMessagesBatchFn    func(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error)
	getInboxDeltaFn           func(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error)
	getUnreadCountFn          func(ctx context.Context, username, sessionID string) (int64, error)
	saveMessageWithOutboxFn   func(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) error
	getMessageByEventIDFn     func(ctx context.Context, eventID int64) (*model.MessageContent, error)
	recallMessageWithOutboxFn func(ctx context.Context, eventID int64, recalledAt time.Time, outbox *model.MessageOutbox) error
	editMessageWithOutboxFn   func(ctx context.Context, eventID int64, newContent string, editedAt time.Time, outbox *model.MessageOutbox) error

	savedMessage      *model.MessageContent
	savedOutbox       *model.MessageOutbox
	savedRecallOutbox *model.MessageOutbox
	savedEditOutbox   *model.MessageOutbox
	editedContent     string
}

func (r *testMessageRepo) SaveMessageContent(ctx context.Context, msg *model.MessageContent) error {
	if r.saveMessageContentFn != nil {
		return r.saveMessageContentFn(ctx, msg)
	}
	return nil
}

func (r *testMessageRepo) SaveInboxBatch(ctx context.Context, inboxes []*model.Inbox) error {
	if r.saveInboxBatchFn != nil {
		return r.saveInboxBatchFn(ctx, inboxes)
	}
	return nil
}

func (r *testMessageRepo) GetHistoryMessages(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error) {
	r.historyCalled = true
	if r.getHistoryMessagesFn != nil {
		return r.getHistoryMessagesFn(ctx, sessionID, beforeSeq, limit)
	}
	return nil, nil
}

func (r *testMessageRepo) GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error) {
	return nil, nil
}

func (r *testMessageRepo) GetLastMessagesBatch(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error) {
	if r.getLastMessagesBatchFn != nil {
		return r.getLastMessagesBatchFn(ctx, sessionIDs)
	}
	return nil, nil
}

func (r *testMessageRepo) GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
	if r.getInboxDeltaFn != nil {
		return r.getInboxDeltaFn(ctx, username, cursorID, limit)
	}
	return nil, nil
}

func (r *testMessageRepo) GetUnreadMessageCount(ctx context.Context, username, sessionID string) (int64, error) {
	if r.getUnreadCountFn != nil {
		return r.getUnreadCountFn(ctx, username, sessionID)
	}
	return 0, nil
}

func (r *testMessageRepo) GetMessageByEventID(ctx context.Context, eventID int64) (*model.MessageContent, error) {
	if r.getMessageByEventIDFn != nil {
		return r.getMessageByEventIDFn(ctx, eventID)
	}
	return nil, fmt.Errorf("message not found")
}

func (r *testMessageRepo) MarkMessageRecalled(ctx context.Context, eventID int64, at time.Time) error {
	return nil
}

func (r *testMessageRepo) UpdateMessageContent(ctx context.Context, eventID int64, newContent string, at time.Time) error {
	return nil
}

func (r *testMessageRepo) RecallMessageWithOutbox(ctx context.Context, eventID int64, recalledAt time.Time, outbox *model.MessageOutbox) error {
	r.savedRecallOutbox = outbox
	if r.recallMessageWithOutboxFn != nil {
		return r.recallMessageWithOutboxFn(ctx, eventID, recalledAt, outbox)
	}
	if outbox != nil && outbox.ID == 0 {
		outbox.ID = 2
	}
	return nil
}

func (r *testMessageRepo) EditMessageWithOutbox(ctx context.Context, eventID int64, newContent string, editedAt time.Time, outbox *model.MessageOutbox) error {
	r.savedEditOutbox = outbox
	r.editedContent = newContent
	if r.editMessageWithOutboxFn != nil {
		return r.editMessageWithOutboxFn(ctx, eventID, newContent, editedAt, outbox)
	}
	if outbox != nil && outbox.ID == 0 {
		outbox.ID = 4
	}
	return nil
}

func (r *testMessageRepo) SaveMessageWithOutbox(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) error {
	r.savedMessage = msg
	r.savedOutbox = outbox
	if r.saveMessageWithOutboxFn != nil {
		return r.saveMessageWithOutboxFn(ctx, msg, outbox)
	}
	if outbox != nil && outbox.ID == 0 {
		outbox.ID = 1
	}
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

type testUserRepo struct {
	getUserByUsernameFn   func(ctx context.Context, username string) (*model.User, error)
	getUsersByUsernamesFn func(ctx context.Context, usernames []string) ([]*model.User, error)
	searchUsersFn         func(ctx context.Context, query string) ([]*model.User, error)
}

func (r *testUserRepo) CreateUser(ctx context.Context, user *model.User) error { return nil }

func (r *testUserRepo) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	if r.getUserByUsernameFn != nil {
		return r.getUserByUsernameFn(ctx, username)
	}
	return nil, fmt.Errorf("user not found")
}

func (r *testUserRepo) GetUsersByUsernames(ctx context.Context, usernames []string) ([]*model.User, error) {
	if r.getUsersByUsernamesFn != nil {
		return r.getUsersByUsernamesFn(ctx, usernames)
	}
	return nil, nil
}

func (r *testUserRepo) SearchUsers(ctx context.Context, query string) ([]*model.User, error) {
	if r.searchUsersFn != nil {
		return r.searchUsersFn(ctx, query)
	}
	return nil, nil
}

func (r *testUserRepo) UpdateUser(ctx context.Context, user *model.User) error { return nil }
func (r *testUserRepo) Close() error                                           { return nil }

type testGenerator struct {
	next int64
}

func (g *testGenerator) Next() (int64, error) {
	return g.next, nil
}

func (g *testGenerator) NextString() (string, error) {
	return fmt.Sprintf("%d", g.next), nil
}

type testSequencer struct {
	nextFn           func(ctx context.Context, key string) (int64, error)
	setFn            func(ctx context.Context, key string, value int64) error
	setIfNotExistsFn func(ctx context.Context, key string, value int64) (bool, error)
}

func (s *testSequencer) Next(ctx context.Context, key string) (int64, error) {
	if s.nextFn != nil {
		return s.nextFn(ctx, key)
	}
	return 1, nil
}

func (s *testSequencer) NextBatch(ctx context.Context, key string, count int) ([]int64, error) {
	ids := make([]int64, 0, count)
	for range count {
		v, err := s.Next(ctx, key)
		if err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	return ids, nil
}

func (s *testSequencer) Set(ctx context.Context, key string, value int64) error {
	if s.setFn != nil {
		return s.setFn(ctx, key, value)
	}
	return nil
}

func (s *testSequencer) SetIfNotExists(ctx context.Context, key string, value int64) (bool, error) {
	if s.setIfNotExistsFn != nil {
		return s.setIfNotExistsFn(ctx, key, value)
	}
	return true, nil
}

type testMQ struct {
	mu        sync.Mutex
	publishFn func(ctx context.Context, topic string, data []byte, opts ...mq.PublishOption) error
}

func (m *testMQ) Publish(ctx context.Context, topic string, data []byte, opts ...mq.PublishOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.publishFn != nil {
		return m.publishFn(ctx, topic, data, opts...)
	}
	return nil
}

func (m *testMQ) Subscribe(ctx context.Context, topic string, handler mq.Handler, opts ...mq.SubscribeOption) (mq.Subscription, error) {
	return nil, nil
}

func (m *testMQ) Close() error { return nil }

func testLogger() clog.Logger {
	return clog.Discard()
}
