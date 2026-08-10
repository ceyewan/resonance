package streaming

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/observability"
)

type HandlerFunc func(context.Context, *mqv1.AgentStreamEvent) error

type progressMessage interface {
	InProgress() error
}

type queuedMessage struct {
	message      mq.Message
	stopProgress func()
}

type Consumer struct {
	mq            mq.MQ
	handler       HandlerFunc
	config        config.ConsumerConfig
	maxDeltaBytes int
	logger        clog.Logger

	ctx          context.Context
	cancel       context.CancelFunc
	subscription mq.Subscription
	jobs         chan queuedMessage
	wg           sync.WaitGroup
	mu           sync.Mutex
	stopped      bool
}

func NewConsumer(mqClient mq.MQ, handler HandlerFunc, consumerConfig config.ConsumerConfig, maxDeltaBytes int, logger clog.Logger) (*Consumer, error) {
	if mqClient == nil || handler == nil || logger == nil || consumerConfig.Topic == "" ||
		consumerConfig.QueueGroup == "" || consumerConfig.DLQTopic == "" || consumerConfig.WorkerCount < 1 ||
		consumerConfig.MaxRetry < 1 || consumerConfig.RetryInterval < 0 || consumerConfig.ProgressInterval < 0 || maxDeltaBytes < 1 {
		return nil, fmt.Errorf("agent stream consumer configuration is invalid")
	}
	if consumerConfig.ProgressInterval == 0 {
		consumerConfig.ProgressInterval = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Consumer{
		mq: mqClient, handler: handler, config: consumerConfig, maxDeltaBytes: maxDeltaBytes, logger: logger,
		ctx: ctx, cancel: cancel, jobs: make(chan queuedMessage),
	}, nil
}

func (c *Consumer) SetName(string) {}

func (c *Consumer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscription != nil || c.stopped {
		return fmt.Errorf("agent stream consumer is already started or stopped")
	}
	for workerID := 0; workerID < c.config.WorkerCount; workerID++ {
		c.wg.Add(1)
		go c.worker()
	}
	subscription, err := c.mq.Subscribe(c.ctx, c.config.Topic, c.receive,
		mq.WithQueueGroup(c.config.QueueGroup), mq.WithManualAck(), mq.WithBatchSize(1), mq.FromBeginning())
	if err != nil {
		c.cancel()
		c.wg.Wait()
		return fmt.Errorf("subscribe agent stream: %w", err)
	}
	c.subscription = subscription
	return nil
}

func (c *Consumer) receive(message mq.Message) error {
	stopProgress := c.startProgressHeartbeat(message)
	select {
	case c.jobs <- queuedMessage{message: message, stopProgress: stopProgress}:
		return nil
	case <-c.ctx.Done():
		stopProgress()
		return c.ctx.Err()
	}
}

func (c *Consumer) worker() {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			c.drain()
			return
		case job, ok := <-c.jobs:
			if !ok {
				return
			}
			if job.message != nil {
				_ = c.handleMessageWithProgress(job.message, job.stopProgress)
			} else {
				job.stopProgress()
			}
		}
	}
}

func (c *Consumer) drain() {
	for {
		select {
		case job, ok := <-c.jobs:
			if !ok {
				return
			}
			if job.message != nil {
				job.stopProgress()
				_ = job.message.Ack()
			} else {
				job.stopProgress()
			}
		default:
			return
		}
	}
}

func (c *Consumer) handleMessage(message mq.Message) error {
	stopProgress := c.startProgressHeartbeat(message)
	return c.handleMessageWithProgress(message, stopProgress)
}

func (c *Consumer) handleMessageWithProgress(message mq.Message, stopProgress func()) error {
	defer stopProgress()

	event := &mqv1.AgentStreamEvent{}
	if err := proto.Unmarshal(message.Data(), event); err != nil {
		return c.deadLetterAndAck(message, "malformed_protobuf", stopProgress)
	}
	if err := ValidateEvent(event, c.maxDeltaBytes); err != nil {
		return c.deadLetterAndAck(message, "invalid_agent_stream", stopProgress)
	}
	ctx := observability.ExtractTraceContext(c.ctx, event.GetTraceHeaders())
	ctx, endSpan := observability.StartSpan(ctx, "consumer.agent_stream.process")
	defer endSpan()

	var lastErr error
	for attempt := 0; attempt < c.config.MaxRetry; attempt++ {
		if attempt > 0 && c.config.RetryInterval > 0 {
			timer := time.NewTimer(time.Duration(c.config.RetryInterval) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				lastErr = ctx.Err()
				attempt = c.config.MaxRetry
				continue
			case <-c.ctx.Done():
				timer.Stop()
				lastErr = c.ctx.Err()
				attempt = c.config.MaxRetry
				continue
			case <-timer.C:
			}
		}
		if err := c.handler(ctx, event); err != nil {
			lastErr = err
			continue
		}
		stopProgress()
		return message.Ack()
	}
	// Streaming is best effort. Bounded retries are followed by ACK/drop so a
	// temporary UI event can never create an unbounded redelivery storm.
	stopProgress()
	if ackErr := message.Ack(); ackErr != nil {
		return errors.Join(lastErr, ackErr)
	}
	return lastErr
}

func (c *Consumer) deadLetterAndAck(message mq.Message, reason string, stopProgress func()) error {
	headers := message.Headers()
	if headers == nil {
		headers = make(mq.Headers)
	}
	headers.Set("x-original-topic", message.Topic())
	headers.Set("x-agent-stream-reason", reason)
	if err := c.mq.Publish(message.Context(), c.config.DLQTopic, message.Data(), mq.WithHeaders(headers)); err != nil {
		stopProgress()
		if nakErr := message.Nak(); nakErr != nil {
			return errors.Join(fmt.Errorf("publish agent stream DLQ: %w", err), nakErr)
		}
		return fmt.Errorf("publish agent stream DLQ: %w", err)
	}
	stopProgress()
	return message.Ack()
}

func (c *Consumer) startProgressHeartbeat(message mq.Message) func() {
	progress, ok := message.(progressMessage)
	if !ok || c.config.ProgressInterval <= 0 {
		return func() {}
	}

	report := func() {
		if err := progress.InProgress(); err != nil {
			c.logger.Warn("failed to extend agent stream ack deadline", clog.Error(err))
		}
	}
	report()

	heartbeatCtx, cancel := context.WithCancel(c.ctx)
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
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func (c *Consumer) Stop() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		c.wg.Wait()
		return nil
	}
	c.stopped = true
	subscription := c.subscription
	c.mu.Unlock()
	var result error
	if subscription != nil {
		drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result = subscription.Drain(drainCtx)
		cancel()
	}
	c.cancel()
	c.wg.Wait()
	return result
}
