package service

import (
	"context"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/im-sdk/model"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthService 认证服务
type AuthService struct {
	logicv1.UnimplementedAuthServiceServer
	userRepo      repo.UserRepo
	authenticator auth.Authenticator
	logger        clog.Logger
}

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo repo.UserRepo,
	authenticator auth.Authenticator,
	logger clog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:      userRepo,
		authenticator: authenticator,
		logger:        logger,
	}
}

// Login 实现 AuthService.Login
func (s *AuthService) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	s.logger.Info("login request", clog.String("username", req.Username))

	// 获取用户
	user, err := s.userRepo.GetUserByUsername(ctx, req.Username)
	if err != nil {
		s.logger.Error("failed to get user", clog.Error(err))
		// 为了安全，不暴露具体错误
		return nil, status.Errorf(codes.Unauthenticated, "invalid username or password")
	}

	// 简单验证密码（实际应该使用 bcrypt）
	// TODO: 生产环境应使用 bcrypt.CompareHashAndPassword
	if user.Password != req.Password {
		s.logger.Warn("invalid password", clog.String("username", req.Username))
		return nil, status.Errorf(codes.Unauthenticated, "invalid username or password")
	}

	// 生成 Token
	token, err := s.authenticator.GenerateToken(ctx, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: user.Username,
		},
		Roles: []string{"user"}, // 默认角色
	})
	if err != nil {
		s.logger.Error("failed to generate token", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}

	resp := &logicv1.LoginResponse{
		AccessToken: token,
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
	// TODO: 生产环境应使用 bcrypt.GenerateFromPassword 加密密码
	user := &model.User{
		Username: req.Username,
		Password: req.Password,
		Nickname: req.Nickname,
	}
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		s.logger.Error("failed to create user", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create user: %v", err)
	}

	// 注册成功后自动登录，生成 Token
	token, err := s.authenticator.GenerateToken(ctx, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: user.Username,
		},
		Roles: []string{"user"},
	})
	if err != nil {
		s.logger.Error("failed to generate token", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}

	resp := &logicv1.RegisterResponse{
		AccessToken: token,
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
	if req.AccessToken == "" {
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}

	// 验证 Token
	claims, err := s.authenticator.ValidateToken(ctx, req.AccessToken)
	if err != nil {
		s.logger.Debug("invalid token", clog.Error(err))
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}

	username := claims.RegisteredClaims.Subject
	if username == "" {
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}

	// 验证用户存在（可选，取决于是否相信 Token 签名）
	// 为了确保用户未被封禁或删除，建议查库
	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		s.logger.Debug("user not found for valid token", clog.String("username", username))
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
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
