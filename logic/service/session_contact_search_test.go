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
)

func TestSessionService_GetContactList_Success(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getContactListFn: func(ctx context.Context, username string) ([]*model.User, error) {
			require.Equal(t, "alice", username)
			return []*model.User{
				{Username: "bob", Nickname: "Bobby", Avatar: "https://a/b.png"},
				{Username: "carol", Nickname: "Carol", Avatar: ""},
			}, nil
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.GetContactList(newTestIncomingContext("alice"), &logicv1.GetContactListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Contacts, 2)
	require.Equal(t, "bob", resp.Contacts[0].Username)
	require.Equal(t, "Bobby", resp.Contacts[0].Nickname)
	require.Equal(t, "https://a/b.png", resp.Contacts[0].AvatarUrl)
	require.Equal(t, "carol", resp.Contacts[1].Username)
}

func TestSessionService_GetContactList_Failed(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getContactListFn: func(ctx context.Context, username string) ([]*model.User, error) {
			return nil, errors.New("db failed")
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.GetContactList(newTestIncomingContext("alice"), &logicv1.GetContactListRequest{})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestSessionService_SearchUser_Unauthenticated(t *testing.T) {
	userRepo := &testUserRepo{
		searchUsersFn: func(ctx context.Context, query string) ([]*model.User, error) {
			t.Fatalf("unauthenticated should not call repo")
			return nil, nil
		},
	}
	svc := NewSessionService(&testSessionRepo{}, &testMessageRepo{}, userRepo, nil, nil, nil, nil, testLogger())

	_, err := svc.SearchUser(context.Background(), &logicv1.SearchUserRequest{Query: "bob"})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestSessionService_SearchUser_Success(t *testing.T) {
	userRepo := &testUserRepo{
		searchUsersFn: func(ctx context.Context, query string) ([]*model.User, error) {
			require.Equal(t, "bo", query)
			return []*model.User{
				{Username: "bob", Nickname: "Bobby", Avatar: "a.png"},
			}, nil
		},
	}
	svc := NewSessionService(&testSessionRepo{}, &testMessageRepo{}, userRepo, nil, nil, nil, nil, testLogger())

	resp, err := svc.SearchUser(newTestIncomingContext("alice"), &logicv1.SearchUserRequest{Query: "bo"})
	require.NoError(t, err)
	require.Len(t, resp.Users, 1)
	require.Equal(t, "bob", resp.Users[0].Username)
	require.Equal(t, "Bobby", resp.Users[0].Nickname)
	require.Equal(t, "a.png", resp.Users[0].AvatarUrl)
}

func TestSessionService_SearchUser_Failed(t *testing.T) {
	userRepo := &testUserRepo{
		searchUsersFn: func(ctx context.Context, query string) ([]*model.User, error) {
			return nil, errors.New("db failed")
		},
	}
	svc := NewSessionService(&testSessionRepo{}, &testMessageRepo{}, userRepo, nil, nil, nil, nil, testLogger())

	_, err := svc.SearchUser(newTestIncomingContext("alice"), &logicv1.SearchUserRequest{Query: "x"})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}
