package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/xerrors"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/observability"
)

// HandlerFunc 消息处理函数
type HandlerFunc func(context.Context, *mqv1.MQEvent) error

type progressMessage interface {
	InProgress() error
}

type queuedMessage struct {
	message      mq.Message
	stopProgress func()
}

// Consumer MQ 消费者
type Consumer struct {
	mqClient mq.MQ
	handler  HandlerFunc
	config   config.ConsumerConfig
	logger   clog.Logger
	name     string // 消费者名称（用于指标区分）

	subscription mq.Subscription
	jobsCh       chan queuedMessage // 任务通道
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup // 等待所有 worker 退出
	stopOnce     sync.Once
	stopErr      error

	// 指标
	processDuration metrics.Histogram // 处理耗时
}

// NewConsumer 创建消费者
func NewConsumer(
	mqClient mq.MQ,
	handler HandlerFunc,
	config config.ConsumerConfig,
	logger clog.Logger,
) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())

	if config.WorkerCount <= 0 {
		config.WorkerCount = 10 // 默认 10 个 worker
	}
	if config.ProgressInterval <= 0 {
		config.ProgressInterval = 10 * time.Second
	}

	return &Consumer{
		mqClient: mqClient,
		handler:  handler,
		config:   config,
		logger:   logger,
		jobsCh:   make(chan queuedMessage),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// SetName 设置消费者名称
func (c *Consumer) SetName(name string) {
	c.name = name
}

// SetProcessDuration 设置处理耗时指标
func (c *Consumer) SetProcessDuration(histogram metrics.Histogram) {
	c.processDuration = histogram
}

// Start 启动消费者
func (c *Consumer) Start() error {
	c.logger.Info("starting consumer",
		clog.String("topic", c.config.Topic),
		clog.String("queue_group", c.config.QueueGroup),
		clog.Int("worker_count", c.config.WorkerCount))

	// 1. 启动 Worker Pool
	for i := 0; i < c.config.WorkerCount; i++ {
		c.wg.Add(1)
		go c.worker(i)
	}

	// 2. 使用队列订阅（负载均衡）
	sub, err := c.mqClient.Subscribe(c.ctx, c.config.Topic, c.receiveMessage,
		mq.WithQueueGroup(c.config.QueueGroup),
		mq.WithManualAck(),
		// Keep the pull buffer instance-local. WithMaxInflight would mutate the
		// shared durable's cluster-wide MaxAckPending and cap horizontal scaling.
		mq.WithBatchSize(1),
		mq.FromBeginning(),
	)
	if err != nil {
		c.cancel()
		c.wg.Wait()
		return xerrors.Wrapf(err, "failed to subscribe to topic %s", c.config.Topic)
	}

	c.subscription = sub
	c.logger.Info("consumer started")
	return nil
}

// receiveMessage 接收消息并放入任务通道。
func (c *Consumer) receiveMessage(msg mq.Message) error {
	stopProgress := c.startProgressHeartbeat(c.ctx, msg)
	select {
	case c.jobsCh <- queuedMessage{message: msg, stopProgress: stopProgress}:
		return nil
	case <-c.ctx.Done():
		stopProgress()
		return c.ctx.Err()
	}
}

// worker 工作协程
func (c *Consumer) worker(id int) {
	defer c.wg.Done()
	c.logger.Debug("worker started", clog.Int("worker_id", id))

	for {
		select {
		case job, ok := <-c.jobsCh:
			if !ok {
				c.logger.Debug("jobs channel closed", clog.Int("worker_id", id))
				return
			}
			if job.message == nil {
				job.stopProgress()
				continue
			}
			if err := c.handleMessageWithProgress(c.ctx, job.message, job.stopProgress); err != nil {
				c.logger.Warn("worker failed to handle message", clog.Int("worker_id", id), clog.Error(err))
			}
		case <-c.ctx.Done():
			// 优雅关闭：处理完 jobsCh 中剩余的消息
			c.drainJobs()
			c.logger.Debug("worker stopped", clog.Int("worker_id", id))
			return
		}
	}
}

// drainJobs 处理剩余的任务
func (c *Consumer) drainJobs() {
	for {
		select {
		case job, ok := <-c.jobsCh:
			if !ok {
				return
			}
			if job.message == nil {
				job.stopProgress()
				continue
			}
			if err := c.handleMessageWithProgress(c.ctx, job.message, job.stopProgress); err != nil {
				c.logger.Warn("drain job failed", clog.Error(err))
			}
		default:
			// 队列已空
			return
		}
	}
}

// handleMessage 处理单条消息
func (c *Consumer) handleMessage(ctx context.Context, msg mq.Message) error {
	stopProgress := c.startProgressHeartbeat(ctx, msg)
	return c.handleMessageWithProgress(ctx, msg, stopProgress)
}

func (c *Consumer) handleMessageWithProgress(ctx context.Context, msg mq.Message, stopProgress func()) error {
	start := time.Now()
	defer stopProgress()

	// 1. 解析 MQEvent
	event := &mqv1.MQEvent{}
	if err := proto.Unmarshal(msg.Data(), event); err != nil {
		c.logger.Error("failed to unmarshal mq event, routing to DLQ",
			clog.String("dlq_topic", c.config.DLQTopic),
			clog.Int("data_len", len(msg.Data())),
			clog.Error(err))

		// 无法解析的消息重试无意义，转投死信队列保留原始字节供人工排查
		headers := msg.Headers()
		if headers == nil {
			headers = make(mq.Headers)
		}
		headers.Set("x-original-topic", msg.Topic())
		headers.Set("x-error", err.Error())
		if dlqErr := c.mqClient.Publish(ctx, c.config.DLQTopic, msg.Data(), mq.WithHeaders(headers)); dlqErr != nil {
			c.logger.Error("failed to publish malformed message to DLQ",
				clog.String("dlq_topic", c.config.DLQTopic),
				clog.Error(dlqErr))
			stopProgress()
			if nakErr := msg.Nak(); nakErr != nil {
				return xerrors.Join(dlqErr, nakErr)
			}
			return dlqErr
		}
		stopProgress()
		if ackErr := msg.Ack(); ackErr != nil {
			c.logger.Warn("failed to ack malformed mq event", clog.Error(ackErr))
			return ackErr
		}
		return nil
	}

	// 2. 提取 Trace Context（双重来源，优先级递减）
	// 优先级 1: 从 PushEvent.trace_headers 提取（protobuf 字段，最可靠）
	// 优先级 2: 从 MQ Message Headers 提取（NATS 原生 Headers，兜底方案）
	ctx = observability.ExtractTraceContext(ctx, event.TraceHeaders)

	// 如果 protobuf 中没有 Trace Context，尝试从 MQ Headers 提取
	if !observability.HasTraceContext(ctx) && msg.Headers() != nil {
		ctx = observability.ExtractTraceContext(ctx, msg.Headers())
	}

	// 3. 创建处理 Span（如果 Trace 已启用）
	spanName := "consumer.process"
	if c.name != "" {
		spanName = "consumer." + c.name + ".process"
	}
	ctx, endSpan := observability.StartSpan(ctx, spanName,
		attribute.Int64("event_id", event.GetEvent().GetEventId()),
		attribute.String("session_id", event.GetEvent().GetSessionId()),
		attribute.String("from_username", event.GetEvent().GetFromUsername()),
	)
	defer endSpan()

	c.logger.Debug("processing mq event",
		clog.Int64("event_id", event.GetEvent().GetEventId()),
		clog.String("session_id", event.GetEvent().GetSessionId()))

	// 4. 处理消息（带重试）
	if err := c.processWithRetry(ctx, event); err != nil {
		c.logger.Error("failed to process push event after retries",
			clog.Int64("event_id", event.GetEvent().GetEventId()),
			clog.Error(err))

		// 记录失败指标
		c.recordMetrics(ctx, start, "fail")

		stopProgress()
		if nakErr := msg.Nak(); nakErr != nil {
			c.logger.Warn("failed to nak mq event", clog.Error(nakErr))
			return xerrors.Join(err, nakErr)
		}
		return err
	}

	// 5. 处理成功，Ack 确认
	stopProgress()
	if err := msg.Ack(); err != nil {
		c.logger.Warn("failed to ack mq event", clog.Error(err))
		return err
	}
	c.logger.Debug("mq event processed successfully",
		clog.Int64("event_id", event.GetEvent().GetEventId()))

	// 记录成功指标
	c.recordMetrics(ctx, start, "success")

	return nil
}

// processWithRetry 带重试的处理逻辑
func (c *Consumer) processWithRetry(ctx context.Context, event *mqv1.MQEvent) error {
	var lastErr error

	for i := 0; i < c.config.MaxRetry; i++ {
		if i > 0 {
			c.logger.Warn("retrying mq event",
				clog.Int64("event_id", event.GetEvent().GetEventId()),
				clog.Int("attempt", i+1),
				clog.Int("max_retry", c.config.MaxRetry))
			timer := time.NewTimer(time.Duration(c.config.RetryInterval) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-c.ctx.Done():
				timer.Stop()
				return c.ctx.Err()
			case <-timer.C:
			}
		}

		// 调用注入的处理函数
		if err := c.handler(ctx, event); err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	return lastErr
}

func (c *Consumer) startProgressHeartbeat(ctx context.Context, msg mq.Message) func() {
	progress, ok := msg.(progressMessage)
	if !ok || c.config.ProgressInterval <= 0 {
		return func() {}
	}

	report := func() {
		if err := progress.InProgress(); err != nil {
			c.logger.Warn("failed to extend mq ack deadline", clog.Error(err))
		}
	}
	report()

	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(c.config.ProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				report()
			case <-heartbeatCtx.Done():
				return
			case <-c.ctx.Done():
				return
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// recordMetrics 记录处理指标
func (c *Consumer) recordMetrics(ctx context.Context, start time.Time, status string) {
	duration := time.Since(start)

	// 使用传入的 histogram 或默认指标
	if c.processDuration != nil {
		labels := []metrics.Label{
			metrics.L("status", status),
			metrics.L("queue_group", c.config.QueueGroup),
		}
		c.processDuration.Record(ctx, duration.Seconds(), labels...)
	}

	// 如果是 storage consumer，使用专门的指标
	if c.name == "storage" {
		observability.RecordStorageProcess(ctx, duration,
			metrics.L("status", status),
			metrics.L("queue_group", c.config.QueueGroup),
		)
	}

	// 如果是 push consumer，使用专门的指标
	if c.name == "push" {
		observability.RecordPushProcess(ctx, duration,
			metrics.L("status", status),
			metrics.L("queue_group", c.config.QueueGroup),
		)
	}
}

// Stop 停止消费者
func (c *Consumer) Stop() error {
	c.stopOnce.Do(func() {
		c.logger.Info("stopping consumer")
		if c.subscription != nil {
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			c.stopErr = c.subscription.Drain(drainCtx)
			cancel()
		}
		c.cancel()
		c.wg.Wait()
		c.logger.Info("consumer stopped")
	})
	return c.stopErr
}
