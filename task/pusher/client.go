package pusher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/task/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// PushTask 推送任务
type PushTask struct {
	ToUsernames []string
	Message     *gatewayv1.PushMessage
}

// GatewayClient 单个 Gateway 的推送客户端
// 每个 Gateway 对应一个 Client，维护独立的推送队列和多个并发 pushLoop
type GatewayClient struct {
	conn        *grpc.ClientConn
	client      gatewayv1.PushServiceClient
	id          string
	pushQueue   chan *PushTask
	pusherCount int // 并发推送协程数
	logger      clog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	wg          *sync.WaitGroup
}

// NewClient 创建 Gateway 客户端
func NewClient(addr string, id string, queueSize int, pusherCount int, logger clog.Logger) (*GatewayClient, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 建立连接（使用 grpc.NewClient 替代废弃的 grpc.Dial）
	conn, err := grpc.NewClient(addr,
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
		conn:        conn,
		client:      gatewayv1.NewPushServiceClient(conn),
		id:          id,
		pushQueue:   make(chan *PushTask, queueSize),
		pusherCount: pusherCount,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
		wg:          &sync.WaitGroup{},
	}

	// 启动多个并发推送 loop
	for i := 0; i < pusherCount; i++ {
		client.wg.Add(1)
		go client.pushLoop(i)
	}

	return client, nil
}

// Enqueue 将推送任务加入队列（非阻塞）
func (c *GatewayClient) Enqueue(task *PushTask) error {
	select {
	case c.pushQueue <- task:
		// 更新队列深度指标
		observability.SetGatewayQueueDepth(context.Background(), c.id, len(c.pushQueue))
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
func (c *GatewayClient) pushLoop(workerID int) {
	defer c.wg.Done()
	c.logger.Debug("pusher started", clog.String("gateway_id", c.id), clog.Int("worker_id", workerID))

	for {
		select {
		case <-c.ctx.Done():
			// 优雅关闭：先处理完队列中剩余的消息
			c.drainQueue(workerID)
			c.logger.Debug("pusher stopped", clog.String("gateway_id", c.id), clog.Int("worker_id", workerID))
			return
		case task := <-c.pushQueue:
			if task != nil {
				c.doPush(task)
				// 消费后更新队列深度指标
				observability.SetGatewayQueueDepth(context.Background(), c.id, len(c.pushQueue))
			}
		}
	}
}

// drainQueue 处理队列中剩余的消息
func (c *GatewayClient) drainQueue(workerID int) {
	drained := 0
	for {
		select {
		case task := <-c.pushQueue:
			if task != nil {
				c.doPush(task)
				drained++
			}
		default:
			// 队列已空
			if drained > 0 {
				c.logger.Info("pusher drained queue",
					clog.String("gateway_id", c.id),
					clog.Int("worker_id", workerID),
					clog.Int("drained_count", drained))
			}
			return
		}
	}
}

// doPush 执行单次推送（带重试）
func (c *GatewayClient) doPush(task *PushTask) {
	const maxRetry = 3
	const retryDelay = 1 * time.Second

	var lastErr error
	for attempt := 0; attempt < maxRetry; attempt++ {
		if attempt > 0 {
			c.logger.Warn("retrying push",
				clog.String("gateway_id", c.id),
				clog.Int64("msg_id", task.Message.MsgId),
				clog.Int("attempt", attempt+1))
			time.Sleep(retryDelay)
		}

		ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
		req := &gatewayv1.PushRequest{
			ToUsernames: task.ToUsernames,
			Message:     task.Message,
		}

		resp, err := c.client.Push(ctx, req)
		cancel()

		if err != nil {
			lastErr = err
			c.logger.Warn("push attempt failed",
				clog.String("gateway_id", c.id),
				clog.Int64("msg_id", task.Message.MsgId),
				clog.Int("attempt", attempt+1),
				clog.Error(err))
			continue
		}

		if resp.Error != "" {
			c.logger.Error("push error from gateway",
				clog.String("gateway_id", c.id),
				clog.Int64("msg_id", resp.MsgId),
				clog.String("error", resp.Error))
			return // Gateway 返回业务错误，不重试
		}

		if len(resp.FailedUsernames) > 0 {
			c.logger.Warn("partial push failure",
				clog.String("gateway_id", c.id),
				clog.Int64("msg_id", resp.MsgId),
				clog.Int("failed_count", len(resp.FailedUsernames)))
		}

		c.logger.Debug("push success",
			clog.String("gateway_id", c.id),
			clog.Int64("msg_id", task.Message.MsgId),
			clog.Int("user_count", len(task.ToUsernames)))
		return // 成功
	}

	// 重试耗尽，记录错误
	c.logger.Error("push failed after retries",
		clog.String("gateway_id", c.id),
		clog.Int64("msg_id", task.Message.MsgId),
		clog.Int("user_count", len(task.ToUsernames)),
		clog.Error(lastErr))
}

// Close 关闭连接
func (c *GatewayClient) Close() error {
	c.logger.Info("closing gateway client", clog.String("gateway_id", c.id))

	// 1. 停止接收新任务
	close(c.pushQueue)

	// 2. 取消 context，触发 pushLoop 优雅退出（会先 drain 队列）
	c.cancel()

	// 3. 等待 loop 处理完剩余消息并退出
	c.wg.Wait()

	// 4. 关闭 gRPC 连接
	return c.conn.Close()
}

// QueueSize 返回当前队列长度
func (c *GatewayClient) QueueSize() int {
	return len(c.pushQueue)
}
