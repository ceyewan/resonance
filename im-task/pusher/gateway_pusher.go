package pusher

import (
	"context"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/im-api/gen/go/gateway/v1/gatewayv1connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GatewayPusher Gateway 推送客户端
type GatewayPusher struct {
	clients map[string]*GatewayClient // gateway_addr -> client
	mu      sync.RWMutex
	logger  clog.Logger
}

// GatewayClient 单个 Gateway 的客户端
type GatewayClient struct {
	addr   string
	conn   *grpc.ClientConn
	stream *connect.BidiStreamForClient[gatewayv1.PushMessageRequest, gatewayv1.PushMessageResponse]
	mu     sync.Mutex
	logger clog.Logger
}

// NewGatewayPusher 创建 Gateway 推送器
func NewGatewayPusher(gatewayAddrs []string, logger clog.Logger) (*GatewayPusher, error) {
	p := &GatewayPusher{
		clients: make(map[string]*GatewayClient),
		logger:  logger,
	}

	// 初始化所有 Gateway 客户端
	for _, addr := range gatewayAddrs {
		client, err := newGatewayClient(addr, logger)
		if err != nil {
			logger.Error("failed to create gateway client",
				clog.String("addr", addr),
				clog.Error(err))
			continue
		}
		p.clients[addr] = client
	}

	if len(p.clients) == 0 {
		return nil, fmt.Errorf("no available gateway clients")
	}

	return p, nil
}

// newGatewayClient 创建单个 Gateway 客户端
func newGatewayClient(addr string, logger clog.Logger) (*GatewayClient, error) {
	// 建立 gRPC 连接
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	client := &GatewayClient{
		addr:   addr,
		conn:   conn,
		logger: logger,
	}

	// 建立双向流
	if err := client.initStream(); err != nil {
		conn.Close()
		return nil, err
	}

	// 启动接收协程
	go client.receiveResponses()

	return client, nil
}

// initStream 初始化双向流
func (c *GatewayClient) initStream() error {
	pushClient := gatewayv1connect.NewPushServiceClient(c.conn, c.addr)
	stream := pushClient.PushMessage(context.Background())
	c.stream = stream
	return nil
}

// receiveResponses 接收推送响应
func (c *GatewayClient) receiveResponses() {
	for {
		resp, err := c.stream.Receive()
		if err != nil {
			c.logger.Error("failed to receive push response",
				clog.String("gateway", c.addr),
				clog.Error(err))
			// 尝试重连
			c.mu.Lock()
			c.initStream()
			c.mu.Unlock()
			return
		}

		if resp.Error != "" {
			c.logger.Warn("push failed",
				clog.String("gateway", c.addr),
				clog.Int64("msg_id", resp.MsgId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("push success",
				clog.String("gateway", c.addr),
				clog.Int64("msg_id", resp.MsgId))
		}
	}
}

// Push 推送消息
func (c *GatewayClient) Push(ctx context.Context, username string, msg *gatewayv1.PushMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req := &gatewayv1.PushMessageRequest{
		ToUsername: username,
		Message:    msg,
	}

	if err := c.stream.Send(req); err != nil {
		c.logger.Error("failed to send push request",
			clog.String("gateway", c.addr),
			clog.Error(err))
		return err
	}

	return nil
}

// PushToUser 推送消息给指定用户
func (p *GatewayPusher) PushToUser(ctx context.Context, gatewayAddr string, username string, msg *gatewayv1.PushMessage) error {
	p.mu.RLock()
	client, ok := p.clients[gatewayAddr]
	p.mu.RUnlock()

	if !ok {
		return fmt.Errorf("gateway client not found: %s", gatewayAddr)
	}

	return client.Push(ctx, username, msg)
}

// Close 关闭所有连接
func (p *GatewayPusher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, client := range p.clients {
		if client.stream != nil {
			client.stream.CloseRequest()
		}
		if client.conn != nil {
			client.conn.Close()
		}
	}

	return nil
}

