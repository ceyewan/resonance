package service

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/im-sdk/model"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthService 认证服务
type AuthService struct {
	logicv1.UnimplementedAuthServiceServer
	userRepo repo.UserRepo
	logger   clog.Logger
}

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo repo.UserRepo,
	logger clog.Logger,
) *AuthService {
	return &AuthService{
		userRepo: userRepo,
		logger:   logger,
	}
}

// Login 实现 AuthService.Login
func (s *AuthService) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	s.logger.Info("login request", clog.String("username", req.Username))

	// 获取用户
	user, err := s.userRepo.GetUserByUsername(ctx, req.Username)
	if err != nil {
		s.logger.Error("failed to get user", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get user")
	}

	// 简单验证密码（实际应该使用 bcrypt）
	if user.Password != req.Password {
		s.logger.Warn("invalid password", clog.String("username", req.Username))
		return nil, status.Errorf(codes.Unauthenticated, "invalid password")
	}

	// TODO: 生成 Token（暂时返回用户名作为 token）
	resp := &logicv1.LoginResponse{
		AccessToken: req.Username + "-token", // 简化实现
		User: &commonv1.User{
			Username:  user.Username,
			Nickname:  user.Nickname,
			AvatarUrl: user.Avatar,
		},
	}

	return resp, nil
}

// Register 实现 AuthService.Register
func (s *AuthService) Register(ctx context.Context, req *logicv1.RegisterRequest) (*logicv1.RegisterResponse, error) {
	s.logger.Info("register request", clog.String("username", req.Username))

	// 创建用户
	user := &model.User{
		Username: req.Username,
		Password: req.Password, // 实际应该加密
		Nickname: req.Nickname,
	}
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		s.logger.Error("failed to create user", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create user")
	}

	resp := &logicv1.RegisterResponse{
		AccessToken: req.Username + "-token", // 简化实现
		User: &commonv1.User{
			Username:  user.Username,
			Nickname:  user.Nickname,
			AvatarUrl: user.Avatar,
		},
	}

	return resp, nil
}

// ValidateToken 实现 AuthService.ValidateToken
func (s *AuthService) ValidateToken(ctx context.Context, req *logicv1.ValidateTokenRequest) (*logicv1.ValidateTokenResponse, error) {
	// 简化实现：从 token 中提取 username
	username := ""
	if len(req.AccessToken) > 6 && req.AccessToken[len(req.AccessToken)-6:] == "-token" {
		username = req.AccessToken[:len(req.AccessToken)-6]
	}

	if username == "" {
		return &logicv1.ValidateTokenResponse{
			Valid: false,
		}, nil
	}

	// 验证用户存在
	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		return &logicv1.ValidateTokenResponse{
			Valid: false,
		}, nil
	}

	return &logicv1.ValidateTokenResponse{
		Valid: true,
		User: &commonv1.User{
			Username:  user.Username,
			Nickname:  user.Nickname,
			AvatarUrl: user.Avatar,
		},
		Username: username,
	}, nil
}
