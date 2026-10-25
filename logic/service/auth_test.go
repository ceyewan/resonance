package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
)

func TestAuthService_BotCannotLogin(t *testing.T) {
	users := &testUserRepo{getUserByUsernameFn: func(context.Context, string) (*model.User, error) {
		return &model.User{Username: "pilot", Kind: model.UserKindAgentBot}, nil
	}}
	s := NewAuthService(users, nil, nil, nil, testLogger())
	_, err := s.Login(context.Background(), &logicv1.LoginRequest{Username: "pilot", Password: "anything"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
func TestScopesForSystemRolesOnlyExposesIMPermissions(t *testing.T) {
	scopes, err := ScopesForSystemRoles([]string{model.SystemRoleUser})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{model.ScopeChatUse, model.ScopeProfileSelfRead}, scopes)
	_, err = ScopesForSystemRoles([]string{"unknown"})
	require.Error(t, err)
}
