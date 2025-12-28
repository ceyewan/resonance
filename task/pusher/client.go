package pusher

import (
	"context"
	"fmt"
	"time"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GatewayClient 单个 Gateway 的推送客户端
type GatewayClient struct {
	conn   *grpc.ClientConn
	client gatewayv1.PushServiceClient
	id     string
}

// NewClient 创建 Gateway 客户端
func NewClient(addr string, id string) (*GatewayClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect gateway %s: %w", addr, err)
	}

	return &GatewayClient{
		conn:   conn,
		client: gatewayv1.NewPushServiceClient(conn),
		id:     id,
	}, nil
}

// PushBatch 批量推送消息
func (c *GatewayClient) PushBatch(ctx context.Context, toUsernames []string, msg *gatewayv1.PushMessage) error {
	stream, err := c.client.PushMessage(ctx)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}

	req := &gatewayv1.PushMessageRequest{
		ToUsernames: toUsernames,
		Message:     msg,
	}

	if err := stream.Send(req); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("failed to close stream: %w", err)
	}

	// 接收响应
	resp, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv failed: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("push error from gateway: %s", resp.Error)
	}

	// 检查是否有失败的用户（目前暂不处理，直接返回成功或记录日志）
	if len(resp.FailedUsernames) > 0 {
		return fmt.Errorf("partial failure: %d users failed to receive message", len(resp.FailedUsernames))
	}

	return nil
}

// Close 关闭连接
func (c *GatewayClient) Close() error {
	return c.conn.Close()
}
