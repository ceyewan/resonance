package pusher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// PushTask 推送任务
type PushTask struct {
	ToUsernames []string
	Message     *gatewayv1.PushMessage
}

// GatewayClient 单个 Gateway 的推送客户端
// 每个 Gateway 对应一个 Client，维护独立的推送队列和 loop
type GatewayClient struct {
	conn      *grpc.ClientConn
	client    gatewayv1.PushServiceClient
	id        string
	pushQueue chan *PushTask
	logger    clog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	wg        *sync.WaitGroup
}

// NewClient 创建 Gateway 客户端
func NewClient(addr string, id string, queueSize int, logger clog.Logger) (*GatewayClient, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 建立连接（不阻塞，延迟到第一次调用）
	conn, err := grpc.Dial(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(4*1024*1024), // 4MB
		),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect gateway %s: %w", addr, err)
	}

	client := &GatewayClient{
		conn:      conn,
		client:    gatewayv1.NewPushServiceClient(conn),
		id:        id,
		pushQueue: make(chan *PushTask, queueSize),
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
		wg:        &sync.WaitGroup{},
	}

	// 启动推送 loop
	client.wg.Add(1)
	go client.pushLoop()

	return client, nil
}

// Enqueue 将推送任务加入队列（非阻塞）
func (c *GatewayClient) Enqueue(task *PushTask) error {
	select {
	case c.pushQueue <- task:
		return nil
	default:
		return fmt.Errorf("gateway %s queue full", c.id)
	}
}

// EnqueueBlocking 将推送任务加入队列（阻塞直到有空位）
func (c *GatewayClient) EnqueueBlocking(task *PushTask) {
	c.pushQueue <- task
}

// pushLoop 持续从队列取消息并推送
func (c *GatewayClient) pushLoop() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case task := <-c.pushQueue:
			c.doPush(task)
		}
	}
}

// doPush 执行单次推送
func (c *GatewayClient) doPush(task *PushTask) {
	ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
	defer cancel()

	req := &gatewayv1.PushRequest{
		ToUsernames: task.ToUsernames,
		Message:     task.Message,
	}

	resp, err := c.client.Push(ctx, req)
	if err != nil {
		c.logger.Error("push failed",
			clog.String("gateway_id", c.id),
			clog.Int64("msg_id", task.Message.MsgId),
			clog.Int("user_count", len(task.ToUsernames)),
			clog.Error(err))
		return
	}

	if resp.Error != "" {
		c.logger.Error("push error from gateway",
			clog.String("gateway_id", c.id),
			clog.Int64("msg_id", resp.MsgId),
			clog.String("error", resp.Error))
		return
	}

	if len(resp.FailedUsernames) > 0 {
		c.logger.Warn("partial push failure",
			clog.String("gateway_id", c.id),
			clog.Int64("msg_id", resp.MsgId),
			clog.Int("failed_count", len(resp.FailedUsernames)))
		return
	}

	c.logger.Debug("push success",
		clog.String("gateway_id", c.id),
		clog.Int64("msg_id", task.Message.MsgId),
		clog.Int("user_count", len(task.ToUsernames)))
}

// Close 关闭连接
func (c *GatewayClient) Close() error {
	// 停止接收新任务
	close(c.pushQueue)

	// 取消 context
	c.cancel()

	// 等待 loop 结束
	c.wg.Wait()

	return c.conn.Close()
}

// QueueSize 返回当前队列长度
func (c *GatewayClient) QueueSize() int {
	return len(c.pushQueue)
}
