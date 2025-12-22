package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/im-api/gen/go/gateway/v1/gatewayv1connect"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/internal/gateway"
	"github.com/gin-gonic/gin"
)

// Handler 实现 Gateway 的 HTTP API
type Handler struct {
	logicClient *gateway.LogicClient
	logger      clog.Logger
}

// NewHandler 创建 API Handler
func NewHandler(logicClient *gateway.LogicClient, logger clog.Logger) *Handler {
	return &Handler{
		logicClient: logicClient,
		logger:      logger,
	}
}

// RegisterRoutes 注册路由到 Gin
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// ConnectRPC 路径
	path, handler := gatewayv1connect.NewAuthServiceHandler(h)
	router.Any(path+"*any", gin.WrapH(handler))

	path, handler = gatewayv1connect.NewSessionServiceHandler(h)
	router.Any(path+"*any", gin.WrapH(handler))
}

// Login 实现 AuthService.Login
func (h *Handler) Login(
	ctx context.Context,
	req *connect.Request[gatewayv1.LoginRequest],
) (*connect.Response[gatewayv1.LoginResponse], error) {
	h.logger.Info("login request", clog.String("username", req.Msg.Username))

	// 转发到 Logic 服务
	logicReq := &logicv1.LoginRequest{
		Username: req.Msg.Username,
		Password: req.Msg.Password,
	}

	logicResp, err := h.logicClient.Login(ctx, logicReq)
	if err != nil {
		h.logger.Error("login failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 转换响应
	resp := &gatewayv1.LoginResponse{
		AccessToken: logicResp.AccessToken,
		User:        logicResp.User,
	}

	return connect.NewResponse(resp), nil
}

// Register 实现 AuthService.Register
func (h *Handler) Register(
	ctx context.Context,
	req *connect.Request[gatewayv1.RegisterRequest],
) (*connect.Response[gatewayv1.RegisterResponse], error) {
	h.logger.Info("register request", clog.String("username", req.Msg.Username))

	// 转发到 Logic 服务
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

	// 转换响应
	resp := &gatewayv1.RegisterResponse{
		AccessToken: logicResp.AccessToken,
		User:        logicResp.User,
	}

	return connect.NewResponse(resp), nil
}

// Logout 实现 AuthService.Logout
func (h *Handler) Logout(
	ctx context.Context,
	req *connect.Request[gatewayv1.LogoutRequest],
) (*connect.Response[gatewayv1.LogoutResponse], error) {
	h.logger.Info("logout request")

	// 这里可以添加 Token 失效逻辑
	// 目前简单返回成功
	resp := &gatewayv1.LogoutResponse{
		Success: true,
	}

	return connect.NewResponse(resp), nil
}

// GetSessionList 实现 SessionService.GetSessionList
func (h *Handler) GetSessionList(
	ctx context.Context,
	req *connect.Request[gatewayv1.GetSessionListRequest],
) (*connect.Response[gatewayv1.GetSessionListResponse], error) {
	// 验证 Token 并获取用户名
	validateResp, err := h.logicClient.ValidateToken(ctx, req.Msg.AccessToken)
	if err != nil || !validateResp.Valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// 转发到 Logic 服务
	logicResp, err := h.logicClient.GetSessionList(ctx, validateResp.Username)
	if err != nil {
		h.logger.Error("get session list failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 转换响应
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
	// 验证 Token 并获取用户名
	validateResp, err := h.logicClient.ValidateToken(ctx, req.Msg.AccessToken)
	if err != nil || !validateResp.Valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// 转发到 Logic 服务
	logicReq := &logicv1.CreateSessionRequest{
		CreatorUsername: validateResp.Username,
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
	// 验证 Token
	validateResp, err := h.logicClient.ValidateToken(ctx, req.Msg.AccessToken)
	if err != nil || !validateResp.Valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// 转发到 Logic 服务
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
	// 验证 Token
	validateResp, err := h.logicClient.ValidateToken(ctx, req.Msg.AccessToken)
	if err != nil || !validateResp.Valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// 转发到 Logic 服务
	logicResp, err := h.logicClient.GetContactList(ctx, validateResp.Username)
	if err != nil {
		h.logger.Error("get contact list failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 转换响应
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
	// 验证 Token
	validateResp, err := h.logicClient.ValidateToken(ctx, req.Msg.AccessToken)
	if err != nil || !validateResp.Valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// 转发到 Logic 服务
	logicResp, err := h.logicClient.SearchUser(ctx, req.Msg.Query)
	if err != nil {
		h.logger.Error("search user failed", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 转换响应
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
