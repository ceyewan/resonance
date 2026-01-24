package handler

import (
	"context"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/gin-gonic/gin"
)

// Handler 实现 Gateway 的 HTTP API
type Handler struct {
	logicClient *client.Client
	logger      clog.Logger
	authConfig  *middleware.AuthConfig
}

// NewHandler 创建 API Handler
func NewHandler(logicClient *client.Client, logger clog.Logger) *Handler {
	return &Handler{
		logicClient: logicClient,
		logger:      logger,
		authConfig:  middleware.NewAuthConfig(logicClient, logger),
	}
}

// RegisterRoutes 注册路由到 Gin，使用路由分组和中间件
func (h *Handler) RegisterRoutes(router *gin.Engine, opts ...RouteOption) {
	cfg := &RouteConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 创建公共路由组（不需要认证）
	publicGroup := router.Group("")
	if cfg.RecoveryMiddleware != nil {
		publicGroup.Use(cfg.RecoveryMiddleware)
	}
	if cfg.LoggerMiddleware != nil {
		publicGroup.Use(cfg.LoggerMiddleware)
	}
	if cfg.SlowQueryMiddleware != nil {
		publicGroup.Use(cfg.SlowQueryMiddleware)
	}
	if cfg.GlobalRateLimitMiddleware != nil {
		publicGroup.Use(cfg.GlobalRateLimitMiddleware)
	}
	if cfg.IPRateLimitMiddleware != nil {
		publicGroup.Use(cfg.IPRateLimitMiddleware)
	}

	// 注册公开路由（不需要认证）
	h.registerPublicRoutes(publicGroup)

	// 创建认证路由组（需要认证）
	authGroup := router.Group("")
	if cfg.RecoveryMiddleware != nil {
		authGroup.Use(cfg.RecoveryMiddleware)
	}
	if cfg.LoggerMiddleware != nil {
		authGroup.Use(cfg.LoggerMiddleware)
	}
	if cfg.SlowQueryMiddleware != nil {
		authGroup.Use(cfg.SlowQueryMiddleware)
	}
	if cfg.GlobalRateLimitMiddleware != nil {
		authGroup.Use(cfg.GlobalRateLimitMiddleware)
	}
	// 认证中间件
	authGroup.Use(h.authConfig.RequireAuth())
	if cfg.UserRateLimitMiddleware != nil {
		authGroup.Use(cfg.UserRateLimitMiddleware)
	}

	// 注册需要认证的路由
	h.registerAuthRoutes(authGroup)
}

// RequireAuthMiddleware 提供给外部路由使用的认证中间件
func (h *Handler) RequireAuthMiddleware() gin.HandlerFunc {
	return h.authConfig.RequireAuth()
}

// registerPublicRoutes 注册公开路由（不需要认证）
func (h *Handler) registerPublicRoutes(group *gin.RouterGroup) {
	// AuthService: Login, Register
	path, handler := gatewayv1connect.NewAuthServiceHandler(h)
	group.Any(path+"*any", gin.WrapH(handler))
}

// registerAuthRoutes 注册需要认证的路由
func (h *Handler) registerAuthRoutes(group *gin.RouterGroup) {
	// SessionService: 所有接口都需要认证
	path, handler := gatewayv1connect.NewSessionServiceHandler(h)
	group.Any(path+"*any", gin.WrapH(handler))
}

// getUsernameFromContext 从 Context 中获取经过中间件解析的用户名
func (h *Handler) getUsernameFromContext(ctx context.Context) (string, error) {
	username, ok := ctx.Value(middleware.UsernameKey).(string)
	if !ok || username == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, middleware.ErrMissingToken)
	}
	return username, nil
}

// RouteConfig 路由配置
type RouteConfig struct {
	RecoveryMiddleware        gin.HandlerFunc
	LoggerMiddleware          gin.HandlerFunc
	SlowQueryMiddleware       gin.HandlerFunc
	GlobalRateLimitMiddleware gin.HandlerFunc
	IPRateLimitMiddleware     gin.HandlerFunc
	UserRateLimitMiddleware   gin.HandlerFunc
}

// RouteOption 路由选项函数
type RouteOption func(*RouteConfig)

// WithRecovery 设置 Recovery 中间件
func WithRecovery(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.RecoveryMiddleware = middleware
	}
}

// WithLogger 设置 Logger 中间件
func WithLogger(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.LoggerMiddleware = middleware
	}
}

// WithSlowQuery 设置慢查询检测中间件
func WithSlowQuery(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.SlowQueryMiddleware = middleware
	}
}

// WithGlobalRateLimit 设置全局限流中间件
func WithGlobalRateLimit(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.GlobalRateLimitMiddleware = middleware
	}
}

// WithIPRateLimit 设置 IP 限流中间件
func WithIPRateLimit(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.IPRateLimitMiddleware = middleware
	}
}

// WithUserRateLimit 设置用户限流中间件
func WithUserRateLimit(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.UserRateLimitMiddleware = middleware
	}
}

// ==================== AuthService 实现 ====================

// Login 实现 AuthService.Login（公开接口）
func (h *Handler) Login(
	ctx context.Context,
	req *connect.Request[gatewayv1.LoginRequest],
) (*connect.Response[gatewayv1.LoginResponse], error) {
	h.logger.Info("login request", clog.String("username", req.Msg.Username))

	logicReq := &logicv1.LoginRequest{
		Username: req.Msg.Username,
		Password: req.Msg.Password,
	}

	logicResp, err := h.logicClient.Login(ctx, logicReq)
	if err != nil {
		h.logger.Error("login failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &gatewayv1.LoginResponse{
		AccessToken: logicResp.AccessToken,
		User:        logicResp.User,
	}

	return connect.NewResponse(resp), nil
}

// Register 实现 AuthService.Register（公开接口）
func (h *Handler) Register(
	ctx context.Context,
	req *connect.Request[gatewayv1.RegisterRequest],
) (*connect.Response[gatewayv1.RegisterResponse], error) {
	h.logger.Info("register request", clog.String("username", req.Msg.Username))

	logicReq := &logicv1.RegisterRequest{
		Username: req.Msg.Username,
		Password: req.Msg.Password,
		Nickname: req.Msg.Nickname,
	}

	logicResp, err := h.logicClient.Register(ctx, logicReq)
	if err != nil {
		h.logger.Error("register failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &gatewayv1.RegisterResponse{
		AccessToken: logicResp.AccessToken,
		User:        logicResp.User,
	}

	return connect.NewResponse(resp), nil
}

// Logout 实现 AuthService.Logout（公开接口，但通常需要 token）
func (h *Handler) Logout(
	ctx context.Context,
	req *connect.Request[gatewayv1.LogoutRequest],
) (*connect.Response[gatewayv1.LogoutResponse], error) {
	h.logger.Info("logout request")

	resp := &gatewayv1.LogoutResponse{
		Success: true,
	}

	return connect.NewResponse(resp), nil
}

// ==================== SessionService 实现 ====================
// 以下接口需要认证，由路由中间件统一处理

// GetSessionList 实现 SessionService.GetSessionList
func (h *Handler) GetSessionList(
	ctx context.Context,
	req *connect.Request[gatewayv1.GetSessionListRequest],
) (*connect.Response[gatewayv1.GetSessionListResponse], error) {
	username, err := h.getUsernameFromContext(ctx)
	if err != nil {
		return nil, err
	}

	logicResp, err := h.logicClient.GetSessionList(ctx, username)
	if err != nil {
		h.logger.Error("get session list failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	sessions := make([]*gatewayv1.SessionInfo, len(logicResp.Sessions))
	for i, s := range logicResp.Sessions {
		sessions[i] = &gatewayv1.SessionInfo{
			SessionId:   s.SessionId,
			Name:        s.Name,
			Type:        s.Type,
			AvatarUrl:   s.AvatarUrl,
			UnreadCount: s.UnreadCount,
			LastReadSeq: s.LastReadSeq,
			LastMessage: s.LastMessage,
		}
	}

	resp := &gatewayv1.GetSessionListResponse{
		Sessions: sessions,
	}

	return connect.NewResponse(resp), nil
}

// CreateSession 实现 SessionService.CreateSession
func (h *Handler) CreateSession(
	ctx context.Context,
	req *connect.Request[gatewayv1.CreateSessionRequest],
) (*connect.Response[gatewayv1.CreateSessionResponse], error) {
	username, err := h.getUsernameFromContext(ctx)
	if err != nil {
		return nil, err
	}

	logicReq := &logicv1.CreateSessionRequest{
		CreatorUsername: username,
		Members:         req.Msg.Members,
		Name:            req.Msg.Name,
		Type:            req.Msg.Type,
	}

	logicResp, err := h.logicClient.CreateSession(ctx, logicReq)
	if err != nil {
		h.logger.Error("create session failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &gatewayv1.CreateSessionResponse{
		SessionId: logicResp.SessionId,
	}

	return connect.NewResponse(resp), nil
}

// GetRecentMessages 实现 SessionService.GetRecentMessages
func (h *Handler) GetRecentMessages(
	ctx context.Context,
	req *connect.Request[gatewayv1.GetRecentMessagesRequest],
) (*connect.Response[gatewayv1.GetRecentMessagesResponse], error) {
	if _, err := h.getUsernameFromContext(ctx); err != nil {
		return nil, err
	}

	logicReq := &logicv1.GetRecentMessagesRequest{
		SessionId: req.Msg.SessionId,
		Limit:     req.Msg.Limit,
		BeforeSeq: req.Msg.BeforeSeq,
	}

	logicResp, err := h.logicClient.GetRecentMessages(ctx, logicReq)
	if err != nil {
		h.logger.Error("get recent messages failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &gatewayv1.GetRecentMessagesResponse{
		Messages: logicResp.Messages,
	}

	return connect.NewResponse(resp), nil
}

// GetContactList 实现 SessionService.GetContactList
func (h *Handler) GetContactList(
	ctx context.Context,
	req *connect.Request[gatewayv1.GetContactListRequest],
) (*connect.Response[gatewayv1.GetContactListResponse], error) {
	username, err := h.getUsernameFromContext(ctx)
	if err != nil {
		return nil, err
	}

	logicResp, err := h.logicClient.GetContactList(ctx, username)
	if err != nil {
		h.logger.Error("get contact list failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	contacts := make([]*gatewayv1.ContactInfo, len(logicResp.Contacts))
	for i, c := range logicResp.Contacts {
		contacts[i] = &gatewayv1.ContactInfo{
			Username:  c.Username,
			Nickname:  c.Nickname,
			AvatarUrl: c.AvatarUrl,
		}
	}

	resp := &gatewayv1.GetContactListResponse{
		Contacts: contacts,
	}

	return connect.NewResponse(resp), nil
}

// SearchUser 实现 SessionService.SearchUser
func (h *Handler) SearchUser(
	ctx context.Context,
	req *connect.Request[gatewayv1.SearchUserRequest],
) (*connect.Response[gatewayv1.SearchUserResponse], error) {
	if _, err := h.getUsernameFromContext(ctx); err != nil {
		return nil, err
	}

	logicResp, err := h.logicClient.SearchUser(ctx, req.Msg.Query)
	if err != nil {
		h.logger.Error("search user failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	users := make([]*gatewayv1.ContactInfo, len(logicResp.Users))
	for i, u := range logicResp.Users {
		users[i] = &gatewayv1.ContactInfo{
			Username:  u.Username,
			Nickname:  u.Nickname,
			AvatarUrl: u.AvatarUrl,
		}
	}

	resp := &gatewayv1.SearchUserResponse{
		Users: users,
	}

	return connect.NewResponse(resp), nil
}

// UpdateReadPosition 实现 SessionService.UpdateReadPosition
func (h *Handler) UpdateReadPosition(
	ctx context.Context,
	req *connect.Request[gatewayv1.UpdateReadPositionRequest],
) (*connect.Response[gatewayv1.UpdateReadPositionResponse], error) {
	username, err := h.getUsernameFromContext(ctx)
	if err != nil {
		return nil, err
	}

	logicReq := &logicv1.UpdateReadPositionRequest{
		SessionId: req.Msg.SessionId,
		SeqId:     req.Msg.SeqId,
		Username:  username,
	}

	logicResp, err := h.logicClient.UpdateReadPosition(ctx, logicReq)
	if err != nil {
		h.logger.Error("update read position failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &gatewayv1.UpdateReadPositionResponse{
		UnreadCount: logicResp.UnreadCount,
	}

	return connect.NewResponse(resp), nil
}
