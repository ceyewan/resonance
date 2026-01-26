package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/gin-gonic/gin"
)

// HTTPHandler 实现 Gateway 的 HTTP API
type HTTPHandler struct {
	logicClient *client.Client
	logger      clog.Logger
	authConfig  *middleware.AuthConfig
}

// NewHTTPHandler 创建 API Handler
func NewHTTPHandler(logicClient *client.Client, logger clog.Logger) *HTTPHandler {
	return &HTTPHandler{
		logicClient: logicClient,
		logger:      logger,
		authConfig:  middleware.NewAuthConfig(logicClient, logger),
	}
}

// RequireAuthMiddleware 提供给外部路由使用的认证中间件
func (h *HTTPHandler) RequireAuthMiddleware() gin.HandlerFunc {
	return h.authConfig.RequireAuth()
}

// getUsernameFromContext 从 Context 中获取经过中间件解析的用户名
func (h *HTTPHandler) getUsernameFromContext(ctx context.Context) (string, error) {
	username, ok := ctx.Value(middleware.UsernameKey).(string)
	if !ok || username == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, middleware.ErrMissingToken)
	}
	return username, nil
}

// ==================== AuthService 实现 ====================

// Login 实现 AuthService.Login（公开接口）
func (h *HTTPHandler) Login(
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
func (h *HTTPHandler) Register(
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
func (h *HTTPHandler) Logout(
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
func (h *HTTPHandler) GetSessionList(
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
func (h *HTTPHandler) CreateSession(
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
func (h *HTTPHandler) GetRecentMessages(
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
func (h *HTTPHandler) GetContactList(
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
func (h *HTTPHandler) SearchUser(
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
func (h *HTTPHandler) UpdateReadPosition(
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
