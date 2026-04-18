package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestSessionService_UpdateReadPosition_DeniedForNonMember(t *testing.T) {
	sessionRepo := &testSessionRepo{
		updateLastReadFn: func(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
			return repo.ErrSessionMemberNotFound
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.UpdateReadPosition(newTestIncomingContext("mallory"), &logicv1.UpdateReadPositionRequest{
		SessionId: "s_1",
		SeqId:     10,
	})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestSessionService_UpdateReadPosition_UpdateFailed(t *testing.T) {
	sessionRepo := &testSessionRepo{
		updateLastReadFn: func(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
			return errors.New("db down")
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.UpdateReadPosition(newTestIncomingContext("alice"), &logicv1.UpdateReadPositionRequest{
		SessionId: "s_1",
		SeqId:     10,
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestSessionService_UpdateReadPosition_SessionLoadFailedReturnsZeroUnread(t *testing.T) {
	sessionRepo := &testSessionRepo{
		updateLastReadFn: func(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
			return nil
		},
		getSessionFn: func(ctx context.Context, sessionID string) (*model.Session, error) {
			return nil, errors.New("query failed")
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.UpdateReadPosition(newTestIncomingContext("alice"), &logicv1.UpdateReadPositionRequest{
		SessionId: "s_1",
		SeqId:     10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), resp.UnreadCount)
}

func TestSessionService_UpdateReadPosition_FallbackUnreadBySessionMaxSeq(t *testing.T) {
	sessionRepo := &testSessionRepo{
		updateLastReadFn: func(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
			return nil
		},
		getSessionFn: func(ctx context.Context, sessionID string) (*model.Session, error) {
			return &model.Session{SessionID: sessionID, MaxSeqID: 35}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getUnreadCountFn: func(ctx context.Context, username, sessionID string) (int64, error) {
			return 0, errors.New("unread query failed")
		},
	}
	svc := NewSessionService(sessionRepo, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.UpdateReadPosition(newTestIncomingContext("alice"), &logicv1.UpdateReadPositionRequest{
		SessionId: "s_1",
		SeqId:     12,
	})
	require.NoError(t, err)
	require.Equal(t, int64(23), resp.UnreadCount)
}

func TestSessionService_UpdateReadPosition_Success(t *testing.T) {
	sessionRepo := &testSessionRepo{
		updateLastReadFn: func(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
			return nil
		},
		getSessionFn: func(ctx context.Context, sessionID string) (*model.Session, error) {
			return &model.Session{SessionID: sessionID, MaxSeqID: 100}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getUnreadCountFn: func(ctx context.Context, username, sessionID string) (int64, error) {
			return 6, nil
		},
	}
	svc := NewSessionService(sessionRepo, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.UpdateReadPosition(newTestIncomingContext("alice"), &logicv1.UpdateReadPositionRequest{
		SessionId: "s_1",
		SeqId:     94,
	})
	require.NoError(t, err)
	require.Equal(t, int64(6), resp.UnreadCount)
}
