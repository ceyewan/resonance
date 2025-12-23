package service

import (
	"context"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/im-sdk/repo"
)

// AuthService 认证服务
type AuthService struct {
	userRepo  repo.UserRepository
	tokenRepo repo.TokenRepository
	logger    clog.Logger
}

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo repo.UserRepository,
	tokenRepo repo.TokenRepository,
	logger clog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:  userRepo,
		tokenRepo: tokenRepo,
		logger:    logger,
	}
}

// Login 实现 AuthService.Login
func (s *AuthService) Login(
	ctx context.Context,
	req *connect.Request[logicv1.LoginRequest],
) (*connect.Response[logicv1.LoginResponse], error) {
	s.logger.Info("login request", clog.String("username", req.Msg.Username))

	// 验证密码
	valid, err := s.userRepo.ValidatePassword(ctx, req.Msg.Username, req.Msg.Password)
	if err != nil {
		s.logger.Error("failed to validate password", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if !valid {
		s.logger.Warn("invalid password", clog.String("username", req.Msg.Username))
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// 获取用户信息
	user, err := s.userRepo.GetUserByUsername(ctx, req.Msg.Username)
	if err != nil {
		s.logger.Error("failed to get user", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 生成 Token
	token, err := s.tokenRepo.CreateToken(ctx, req.Msg.Username)
	if err != nil {
		s.logger.Error("failed to create token", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.LoginResponse{
		AccessToken: token,
		User:        user,
	}

	return connect.NewResponse(resp), nil
}

// Register 实现 AuthService.Register
func (s *AuthService) Register(
	ctx context.Context,
	req *connect.Request[logicv1.RegisterRequest],
) (*connect.Response[logicv1.RegisterResponse], error) {
	s.logger.Info("register request", clog.String("username", req.Msg.Username))

	// 创建用户
	user, err := s.userRepo.CreateUser(ctx, req.Msg.Username, req.Msg.Password, req.Msg.Nickname)
	if err != nil {
		s.logger.Error("failed to create user", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 生成 Token
	token, err := s.tokenRepo.CreateToken(ctx, req.Msg.Username)
	if err != nil {
		s.logger.Error("failed to create token", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.RegisterResponse{
		AccessToken: token,
		User:        user,
	}

	return connect.NewResponse(resp), nil
}

// ValidateToken 实现 AuthService.ValidateToken
func (s *AuthService) ValidateToken(
	ctx context.Context,
	req *connect.Request[logicv1.ValidateTokenRequest],
) (*connect.Response[logicv1.ValidateTokenResponse], error) {
	// 验证 Token
	username, valid, err := s.tokenRepo.ValidateToken(ctx, req.Msg.AccessToken)
	if err != nil {
		s.logger.Error("failed to validate token", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if !valid {
		resp := &logicv1.ValidateTokenResponse{
			Valid: false,
		}
		return connect.NewResponse(resp), nil
	}

	// 获取用户信息
	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		s.logger.Error("failed to get user", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.ValidateTokenResponse{
		Valid:    true,
		User:     user,
		Username: username,
	}

	return connect.NewResponse(resp), nil
}

