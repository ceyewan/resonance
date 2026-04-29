package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
)

func TestSessionService_GetSessionList_Empty(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionLFn: func(ctx context.Context, username string) ([]*model.Session, error) {
			require.Equal(t, "alice", username)
			return []*model.Session{}, nil
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.GetSessionList(newTestIncomingContext("alice"), &logicv1.GetSessionListRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.Sessions)
}

func TestSessionService_GetSessionList_DirectNicknameUnreadAndLastEvent(t *testing.T) {
	now := time.Now()
	sessionRepo := &testSessionRepo{
		getUserSessionLFn: func(ctx context.Context, username string) ([]*model.Session, error) {
			return []*model.Session{
				{
					SessionID: "single:alice:bob",
					Type:      int(commonv1.SessionType_SESSION_TYPE_DIRECT),
					MaxSeqID:  12,
				},
			}, nil
		},
		getUserSessBatch: func(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error) {
			require.Equal(t, "alice", username)
			require.Equal(t, []string{"single:alice:bob"}, sessionIDs)
			return []*model.SessionMember{
				{SessionID: "single:alice:bob", Username: "alice", LastReadSeq: 9},
			}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getLastMessagesBatchFn: func(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error) {
			require.Equal(t, []string{"single:alice:bob"}, sessionIDs)
			return []*model.MessageContent{
				{
					EventID:        101,
					SessionID:      "single:alice:bob",
					SenderUsername: "bob",
					SeqID:          12,
					Content:        "hi alice",
					MsgType:        int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
					CreatedAt:      now,
				},
			}, nil
		},
		getUnreadCountFn: func(ctx context.Context, username, sessionID string) (int64, error) {
			require.Equal(t, "alice", username)
			require.Equal(t, "single:alice:bob", sessionID)
			return 3, nil
		},
	}
	userRepo := &testUserRepo{
		getUsersByUsernamesFn: func(ctx context.Context, usernames []string) ([]*model.User, error) {
			require.Equal(t, []string{"bob"}, usernames)
			return []*model.User{
				{Username: "bob", Nickname: "Bobby"},
			}, nil
		},
	}
	svc := NewSessionService(sessionRepo, messageRepo, userRepo, nil, nil, nil, nil, testLogger())

	resp, err := svc.GetSessionList(newTestIncomingContext("alice"), &logicv1.GetSessionListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)

	s := resp.Sessions[0]
	require.Equal(t, "single:alice:bob", s.SessionId)
	require.Equal(t, commonv1.SessionType_SESSION_TYPE_DIRECT, s.Type)
	require.Equal(t, "Bobby", s.Name)
	require.Equal(t, int64(3), s.UnreadCount)
	require.Equal(t, int64(9), s.LastReadSeq)
	require.NotNil(t, s.LastEvent)
	require.Equal(t, int64(101), s.LastEvent.EventId)
	require.Equal(t, int64(12), s.LastEvent.SeqId)
	require.Equal(t, "single:alice:bob", s.LastEvent.SessionId)
	require.Equal(t, "bob", s.LastEvent.FromUsername)
	require.Equal(t, "hi alice", s.LastEvent.GetMessage().GetContent())
}

func TestSessionService_GetSessionList_FallbackLastEventWhenNoMessage(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionLFn: func(ctx context.Context, username string) ([]*model.Session, error) {
			return []*model.Session{
				{
					SessionID: "group:1",
					Type:      int(commonv1.SessionType_SESSION_TYPE_GROUP),
					Name:      "Dev",
					MaxSeqID:  27,
				},
			}, nil
		},
		getUserSessBatch: func(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error) {
			return nil, nil
		},
	}
	messageRepo := &testMessageRepo{
		getLastMessagesBatchFn: func(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error) {
			return nil, nil
		},
	}
	svc := NewSessionService(sessionRepo, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.GetSessionList(newTestIncomingContext("alice"), &logicv1.GetSessionListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)

	s := resp.Sessions[0]
	require.Equal(t, "Dev", s.Name)
	require.Equal(t, int64(0), s.UnreadCount)
	require.Equal(t, int64(0), s.LastReadSeq)
	require.NotNil(t, s.LastEvent)
	require.Equal(t, int64(27), s.LastEvent.SeqId)
	require.Equal(t, "group:1", s.LastEvent.SessionId)
	require.Equal(t, commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED, s.LastEvent.GetMessage().GetType())
	require.Equal(t, "", s.LastEvent.GetMessage().GetContent())
}

func TestSessionService_GetSessionList_ClampsUnreadFallback(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionLFn: func(ctx context.Context, username string) ([]*model.Session, error) {
			return []*model.Session{
				{
					SessionID: "group:1",
					Type:      int(commonv1.SessionType_SESSION_TYPE_GROUP),
					Name:      "Dev",
					MaxSeqID:  1,
				},
			}, nil
		},
		getUserSessBatch: func(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{
				{SessionID: "group:1", Username: "alice", LastReadSeq: 2},
			}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getUnreadCountFn: func(ctx context.Context, username, sessionID string) (int64, error) {
			return 0, errors.New("unread query failed")
		},
	}
	svc := NewSessionService(sessionRepo, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.GetSessionList(newTestIncomingContext("alice"), &logicv1.GetSessionListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	require.Equal(t, int64(0), resp.Sessions[0].UnreadCount)
	require.Equal(t, int64(2), resp.Sessions[0].LastReadSeq)
}

func TestSessionService_GetSessionList_ListFailed(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionLFn: func(ctx context.Context, username string) ([]*model.Session, error) {
			return nil, errors.New("db failed")
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.GetSessionList(newTestIncomingContext("alice"), &logicv1.GetSessionListRequest{})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}
