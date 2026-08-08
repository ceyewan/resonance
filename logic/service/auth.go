package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

// AuthService 认证服务
type AuthService struct {
	logicv1.UnimplementedAuthServiceServer
	userRepo      repo.UserRepo
	identityRepo  repo.IdentityRepo
	sessionRepo   repo.SessionRepo
	authenticator auth.Authenticator
	logger        clog.Logger
}

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo repo.UserRepo,
	identityRepo repo.IdentityRepo,
	sessionRepo repo.SessionRepo,
	authenticator auth.Authenticator,
	logger clog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:      userRepo,
		identityRepo:  identityRepo,
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
	if user.Kind != model.UserKindHuman {
		return nil, status.Errorf(codes.Unauthenticated, "invalid username or password")
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		// 日志脱敏：不记录用户名，避免用户枚举攻击
		s.logger.Debug("invalid password", clog.Error(err))
		return nil, status.Errorf(codes.Unauthenticated, "invalid username or password")
	}

	tenantID := strings.TrimSpace(req.TenantId)
	if tenantID == "" {
		tenantID = model.DefaultTenantID
	}
	authorization, scopes, err := s.loadAuthorization(ctx, tenantID, user.Username)
	if err != nil {
		s.logger.Debug("login authorization rejected", clog.Error(err))
		return nil, status.Errorf(codes.Unauthenticated, "invalid username or password")
	}

	// 生成 Token
	tokenPair, err := s.generateTokenPair(ctx, user.Username, authorization, scopes)
	if err != nil {
		s.logger.Error("failed to generate token", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}

	resp := &logicv1.LoginResponse{
		AccessToken: tokenPair.AccessToken,
		User: &commonv1.User{
			Username:  user.Username,
			Nickname:  user.Nickname,
			AvatarUrl: user.Avatar,
		},
		TenantId: tenantID,
		Roles:    authorization.Roles,
		Scopes:   scopes,
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
	membership := &model.TenantMembership{
		TenantID: model.DefaultTenantID,
		Username: user.Username,
		Status:   model.TenantMembershipStatusActive,
		Version:  1,
	}
	roles := []string{model.SystemRoleUser}
	if err := s.identityRepo.CreateIdentity(ctx, user, membership, roles); err != nil {
		s.logger.Error("failed to create user", clog.Error(err))
		return nil, status.Error(codes.Internal, "failed to create user")
	}

	// 注册成功后自动登录，生成 Token
	scopes, err := ScopesForSystemRoles(roles)
	if err != nil {
		s.logger.Error("failed to resolve registration scopes", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}
	tokenPair, err := s.generateTokenPair(ctx, user.Username, &repo.TenantAuthorization{
		Membership: membership,
		Roles:      roles,
	}, scopes)
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
		AccessToken: tokenPair.AccessToken,
		User: &commonv1.User{
			Username:  user.Username,
			Nickname:  user.Nickname,
			AvatarUrl: user.Avatar,
		},
		TenantId: model.DefaultTenantID,
		Roles:    roles,
		Scopes:   scopes,
	}

	return resp, nil
}

// ValidateToken 实现 AuthService.ValidateToken
func (s *AuthService) ValidateToken(ctx context.Context, req *logicv1.ValidateTokenRequest) (*logicv1.ValidateTokenResponse, error) {
	if req.AccessToken == "" {
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}

	// 验证 Token
	claims, err := s.authenticator.ValidateAccessToken(ctx, req.AccessToken)
	if err != nil {
		s.logger.Debug("invalid token", clog.Error(err))
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}

	username := claims.Subject
	if username == "" {
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}
	tenantID, membershipVersion, ok := identityClaims(claims)
	if !ok {
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}

	// 验证用户存在（可选，取决于是否相信 Token 签名）
	// 为了确保用户未被封禁或删除，建议查库
	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		s.logger.Debug("user not found for valid token", clog.String("username", username))
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}
	if user.Kind != model.UserKindHuman {
		return &logicv1.ValidateTokenResponse{Valid: false}, nil
	}
	authorization, scopes, err := s.loadAuthorization(ctx, tenantID, username)
	if err != nil || authorization.Membership.Version != membershipVersion {
		if err != nil {
			s.logger.Debug("token authorization rejected", clog.Error(err))
		}
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
		TenantId: tenantID,
		Roles:    authorization.Roles,
		Scopes:   scopes,
	}, nil
}

func (s *AuthService) loadAuthorization(ctx context.Context, tenantID, username string) (*repo.TenantAuthorization, []string, error) {
	authorization, err := s.identityRepo.ResolveTenantAuthorization(ctx, tenantID, username)
	if err != nil {
		return nil, nil, err
	}
	if authorization == nil || authorization.Membership == nil ||
		authorization.Membership.Status != model.TenantMembershipStatusActive ||
		authorization.Membership.Version < 1 || len(authorization.Roles) == 0 {
		return nil, nil, repo.ErrTenantMembershipNotFound
	}
	authorization.Roles, err = canonicalSystemRoles(authorization.Roles)
	if err != nil {
		return nil, nil, err
	}
	scopes, err := ScopesForSystemRoles(authorization.Roles)
	if err != nil {
		return nil, nil, err
	}
	return authorization, scopes, nil
}

// ResolveUserPrincipal reloads the authoritative tenant membership, roles and
// scopes for a Gateway-signed actor. It intentionally does not trust roles or
// scopes from JWT or gRPC metadata.
func (s *AuthService) ResolveUserPrincipal(ctx context.Context, tenantID, username string) (*UserPrincipal, error) {
	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if user == nil || user.Kind != model.UserKindHuman {
		return nil, repo.ErrTenantMembershipNotFound
	}
	authorization, scopes, err := s.loadAuthorization(ctx, tenantID, username)
	if err != nil {
		return nil, err
	}
	return &UserPrincipal{
		TenantID: tenantID,
		Username: username,
		Version:  authorization.Membership.Version,
		Roles:    append([]string(nil), authorization.Roles...),
		Scopes:   append([]string(nil), scopes...),
	}, nil
}

func (s *AuthService) generateTokenPair(
	ctx context.Context,
	username string,
	authorization *repo.TenantAuthorization,
	scopes []string,
) (*auth.TokenPair, error) {
	return s.authenticator.GenerateTokenPair(ctx, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: username},
		Username:         username,
		Roles:            append([]string(nil), authorization.Roles...),
		Extra: map[string]any{
			"tenant_id":          authorization.Membership.TenantID,
			"scopes":             append([]string(nil), scopes...),
			"membership_version": strconv.FormatInt(authorization.Membership.Version, 10),
		},
	})
}

func identityClaims(claims *auth.Claims) (tenantID string, membershipVersion int64, ok bool) {
	if claims == nil || claims.Extra == nil {
		return "", 0, false
	}
	tenantID, ok = claims.Extra["tenant_id"].(string)
	if !ok || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(tenantID) != tenantID || len(tenantID) > 64 {
		return "", 0, false
	}
	versionText, ok := claims.Extra["membership_version"].(string)
	if !ok {
		return "", 0, false
	}
	membershipVersion, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || membershipVersion < 1 {
		return "", 0, false
	}
	return tenantID, membershipVersion, true
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
