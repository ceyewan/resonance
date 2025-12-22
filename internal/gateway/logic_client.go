package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/im-api/gen/go/logic/v1/logicv1connect"
)

// LogicClient 封装与 Logic 服务的 RPC 调用
type LogicClient struct {
	authClient       logicv1connect.AuthServiceClient
	sessionClient    logicv1connect.SessionServiceClient
	chatClient       logicv1connect.ChatServiceClient
	gatewayOpsClient logicv1connect.GatewayOpsServiceClient

	logger    clog.Logger
	gatewayID string

	// 双向流连接
	chatStream       *connect.BidiStreamForClient[logicv1.SendMessageRequest, logicv1.SendMessageResponse]
	gatewayOpsStream *connect.BidiStreamForClient[logicv1.SyncStateRequest, logicv1.SyncStateResponse]
	streamMu         sync.Mutex
	seqID            int64
}

// NewLogicClient 创建 Logic 客户端
func NewLogicClient(logicAddr string, gatewayID string, logger clog.Logger) (*LogicClient, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	baseURL := fmt.Sprintf("http://%s", logicAddr)

	return &LogicClient{
		authClient:       logicv1connect.NewAuthServiceClient(httpClient, baseURL),
		sessionClient:    logicv1connect.NewSessionServiceClient(httpClient, baseURL),
		chatClient:       logicv1connect.NewChatServiceClient(httpClient, baseURL),
		gatewayOpsClient: logicv1connect.NewGatewayOpsServiceClient(httpClient, baseURL),
		logger:           logger,
		gatewayID:        gatewayID,
	}, nil
}

// Login 调用 Logic 的登录接口
func (c *LogicClient) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	resp, err := c.authClient.Login(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Register 调用 Logic 的注册接口
func (c *LogicClient) Register(ctx context.Context, req *logicv1.RegisterRequest) (*logicv1.RegisterResponse, error) {
	resp, err := c.authClient.Register(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// ValidateToken 验证 Token
func (c *LogicClient) ValidateToken(ctx context.Context, token string) (*logicv1.ValidateTokenResponse, error) {
	resp, err := c.authClient.ValidateToken(ctx, connect.NewRequest(&logicv1.ValidateTokenRequest{
		AccessToken: token,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetSessionList 获取会话列表
func (c *LogicClient) GetSessionList(ctx context.Context, username string) (*logicv1.GetSessionListResponse, error) {
	resp, err := c.sessionClient.GetSessionList(ctx, connect.NewRequest(&logicv1.GetSessionListRequest{
		Username: username,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// CreateSession 创建会话
func (c *LogicClient) CreateSession(ctx context.Context, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	resp, err := c.sessionClient.CreateSession(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetRecentMessages 获取历史消息
func (c *LogicClient) GetRecentMessages(ctx context.Context, req *logicv1.GetRecentMessagesRequest) (*logicv1.GetRecentMessagesResponse, error) {
	resp, err := c.sessionClient.GetRecentMessages(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetContactList 获取联系人列表
func (c *LogicClient) GetContactList(ctx context.Context, username string) (*logicv1.GetContactListResponse, error) {
	resp, err := c.sessionClient.GetContactList(ctx, connect.NewRequest(&logicv1.GetContactListRequest{
		Username: username,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SearchUser 搜索用户
func (c *LogicClient) SearchUser(ctx context.Context, query string) (*logicv1.SearchUserResponse, error) {
	resp, err := c.sessionClient.SearchUser(ctx, connect.NewRequest(&logicv1.SearchUserRequest{
		Query: query,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SendMessage 发送消息到 Logic（通过双向流）
func (c *LogicClient) SendMessage(ctx context.Context, msg *gatewayv1.ChatRequest) (*logicv1.SendMessageResponse, error) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.chatStream == nil {
		stream := c.chatClient.SendMessage(ctx)
		c.chatStream = stream

		// 启动接收协程
		go c.receiveChatResponses()
	}

	// 发送消息
	req := &logicv1.SendMessageRequest{
		SessionId:    msg.SessionId,
		FromUsername: msg.FromUsername,
		ToUsername:   msg.ToUsername,
		Content:      msg.Content,
		Type:         msg.Type,
		Timestamp:    msg.Timestamp,
	}

	if err := c.chatStream.Send(req); err != nil {
		c.chatStream = nil // 重置流
		return nil, err
	}

	// 注意：这里简化处理，实际应该等待对应的响应
	return &logicv1.SendMessageResponse{}, nil
}

// receiveChatResponses 接收聊天消息的响应
func (c *LogicClient) receiveChatResponses() {
	if c.chatStream == nil {
		return
	}

	for {
		resp, err := c.chatStream.Receive()
		if err != nil {
			if err == io.EOF {
				c.logger.Info("chat stream closed")
			} else {
				c.logger.Error("failed to receive chat response", clog.Error(err))
			}
			c.streamMu.Lock()
			c.chatStream = nil
			c.streamMu.Unlock()
			return
		}

		// 处理响应（这里可以添加回调或通知机制）
		if resp.Error != "" {
			c.logger.Error("chat message error",
				clog.Int64("msg_id", resp.MsgId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("chat message sent",
				clog.Int64("msg_id", resp.MsgId),
				clog.Int64("seq_id", resp.SeqId))
		}
	}
}

// SyncUserOnline 同步用户上线状态
func (c *LogicClient) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.gatewayOpsStream == nil {
		stream := c.gatewayOpsClient.SyncState(ctx)
		c.gatewayOpsStream = stream

		// 启动接收协程
		go c.receiveGatewayOpsResponses()
	}

	c.seqID++
	req := &logicv1.SyncStateRequest{
		SeqId:     c.seqID,
		GatewayId: c.gatewayID,
		Event: &logicv1.SyncStateRequest_Online{
			Online: &logicv1.UserOnline{
				Username:  username,
				RemoteIp:  remoteIP,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	if err := c.gatewayOpsStream.Send(req); err != nil {
		c.gatewayOpsStream = nil // 重置流
		return err
	}

	return nil
}

// SyncUserOffline 同步用户下线状态
func (c *LogicClient) SyncUserOffline(ctx context.Context, username string) error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.gatewayOpsStream == nil {
		stream := c.gatewayOpsClient.SyncState(ctx)
		c.gatewayOpsStream = stream

		// 启动接收协程
		go c.receiveGatewayOpsResponses()
	}

	c.seqID++
	req := &logicv1.SyncStateRequest{
		SeqId:     c.seqID,
		GatewayId: c.gatewayID,
		Event: &logicv1.SyncStateRequest_Offline{
			Offline: &logicv1.UserOffline{
				Username:  username,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	if err := c.gatewayOpsStream.Send(req); err != nil {
		c.gatewayOpsStream = nil // 重置流
		return err
	}

	return nil
}

// receiveGatewayOpsResponses 接收网关操作的响应
func (c *LogicClient) receiveGatewayOpsResponses() {
	if c.gatewayOpsStream == nil {
		return
	}

	for {
		resp, err := c.gatewayOpsStream.Receive()
		if err != nil {
			if err == io.EOF {
				c.logger.Info("gateway ops stream closed")
			} else {
				c.logger.Error("failed to receive gateway ops response", clog.Error(err))
			}
			c.streamMu.Lock()
			c.gatewayOpsStream = nil
			c.streamMu.Unlock()
			return
		}

		// 处理响应
		if resp.Error != "" {
			c.logger.Error("gateway ops error",
				clog.Int64("seq_id", resp.SeqId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("gateway ops ack",
				clog.Int64("seq_id", resp.SeqId))
		}
	}
}

// Close 关闭客户端
func (c *LogicClient) Close() error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	if c.chatStream != nil {
		c.chatStream.CloseRequest()
		c.chatStream = nil
	}

	if c.gatewayOpsStream != nil {
		c.gatewayOpsStream.CloseRequest()
		c.gatewayOpsStream = nil
	}

	return nil
}
