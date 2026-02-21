package service

import (
	"context"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthService 认证服务
type AuthService struct {
	logicv1.UnimplementedAuthServiceServer
	userRepo      repo.UserRepo
	sessionRepo   repo.SessionRepo
	authenticator auth.Authenticator
	logger        clog.Logger
}

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo repo.UserRepo,
	sessionRepo repo.SessionRepo,
	authenticator auth.Authenticator,
	logger clog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:      userRepo,
		sessionRepo:   sessionRepo,
		authenticator: authenticator,
		logger:        logger,
	}
}

// Login 实现 AuthService.Login
func (s *AuthService) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	// 日志脱敏：不记录用户名，避免用户枚举攻击
	s.logger.Debug("login request")

	// 获取用户
	user, err := s.userRepo.GetUserByUsername(ctx, req.Username)
	if err != nil {
		s.logger.Error("failed to get user", clog.Error(err))
		// 为了安全，不暴露具体错误
		return nil, status.Errorf(codes.Unauthenticated, "invalid username or password")
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		// 日志脱敏：不记录用户名，避免用户枚举攻击
		s.logger.Debug("invalid password", clog.Error(err))
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
	// 日志脱敏：不记录用户名，避免用户枚举攻击
	s.logger.Debug("register request")

	// 创建用户，对密码进行哈希加密
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Error("failed to hash password", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to process registration")
	}

	user := &model.User{
		Username: req.Username,
		Password: string(hashedPassword),
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

	// 自动加入 Resonance Room 默认群聊
	if err := s.joinDefaultRoom(ctx, user.Username); err != nil {
		s.logger.Warn("failed to join default room", clog.String("username", user.Username), clog.Error(err))
		// 非阻塞，注册仍视为成功
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

// joinDefaultRoom 让新用户自动加入 Resonance Room 默认群聊
// session_id='0' 是通过 `go run main.go -module init` 预创建的系统级群聊
func (s *AuthService) joinDefaultRoom(ctx context.Context, username string) error {
	const defaultSessionID = "0" // Resonance Room 的固定 session_id

	// 检查会话是否存在
	session, err := s.sessionRepo.GetSession(ctx, defaultSessionID)
	if err != nil {
		s.logger.Error("default room not found", clog.String("session_id", defaultSessionID), clog.Error(err))
		return err
	}

	// 添加用户到会话
	member := &model.SessionMember{
		SessionID: defaultSessionID,
		Username:  username,
		Role:      0, // 普通成员
	}
	if err := s.sessionRepo.AddMember(ctx, member); err != nil {
		s.logger.Error("failed to add member to default room",
			clog.String("username", username),
			clog.String("session_id", defaultSessionID),
			clog.Error(err))
		return err
	}

	s.logger.Info("user joined default room",
		clog.String("username", username),
		clog.String("session_id", defaultSessionID),
		clog.String("session_name", session.Name))

	return nil
}
