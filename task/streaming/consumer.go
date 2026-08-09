package streaming

import (
	"context"
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

type Consumer struct {
	mq            mq.MQ
	handler       HandlerFunc
	config        config.ConsumerConfig
	maxDeltaBytes int
	logger        clog.Logger

	ctx          context.Context
	cancel       context.CancelFunc
	subscription mq.Subscription
	jobs         chan mq.Message
	wg           sync.WaitGroup
	mu           sync.Mutex
	stopped      bool
}

func NewConsumer(mqClient mq.MQ, handler HandlerFunc, consumerConfig config.ConsumerConfig, maxDeltaBytes int, logger clog.Logger) (*Consumer, error) {
	if mqClient == nil || handler == nil || logger == nil || consumerConfig.Topic == "" ||
		consumerConfig.QueueGroup == "" || consumerConfig.DLQTopic == "" || consumerConfig.WorkerCount < 1 ||
		consumerConfig.MaxRetry < 1 || consumerConfig.RetryInterval < 0 || maxDeltaBytes < 1 {
		return nil, fmt.Errorf("agent stream consumer configuration is invalid")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Consumer{
		mq: mqClient, handler: handler, config: consumerConfig, maxDeltaBytes: maxDeltaBytes, logger: logger,
		ctx: ctx, cancel: cancel, jobs: make(chan mq.Message, consumerConfig.WorkerCount*10),
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
		mq.WithQueueGroup(c.config.QueueGroup), mq.WithManualAck(), mq.WithMaxInflight(cap(c.jobs)), mq.FromBeginning())
	if err != nil {
		c.cancel()
		c.wg.Wait()
		return fmt.Errorf("subscribe agent stream: %w", err)
	}
	c.subscription = subscription
	return nil
}

func (c *Consumer) receive(message mq.Message) error {
	select {
	case c.jobs <- message:
		return nil
	case <-c.ctx.Done():
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
		case message, ok := <-c.jobs:
			if !ok {
				return
			}
			if message != nil {
				_ = c.handleMessage(message)
			}
		}
	}
}

func (c *Consumer) drain() {
	for {
		select {
		case message, ok := <-c.jobs:
			if !ok {
				return
			}
			if message != nil {
				_ = message.Ack()
			}
		default:
			return
		}
	}
}

func (c *Consumer) handleMessage(message mq.Message) error {
	event := &mqv1.AgentStreamEvent{}
	if err := proto.Unmarshal(message.Data(), event); err != nil {
		return c.deadLetterAndAck(message, "malformed_protobuf")
	}
	if err := ValidateEvent(event, c.maxDeltaBytes); err != nil {
		return c.deadLetterAndAck(message, "invalid_agent_stream")
	}
	ctx := observability.ExtractTraceContext(message.Context(), event.GetTraceHeaders())
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
		return message.Ack()
	}
	// Streaming is best effort. Bounded retries are followed by ACK/drop so a
	// temporary UI event can never create an unbounded redelivery storm.
	if ackErr := message.Ack(); ackErr != nil {
		return errorsJoin(lastErr, ackErr)
	}
	return lastErr
}

func (c *Consumer) deadLetterAndAck(message mq.Message, reason string) error {
	headers := message.Headers()
	if headers == nil {
		headers = make(mq.Headers)
	}
	headers.Set("x-original-topic", message.Topic())
	headers.Set("x-agent-stream-reason", reason)
	if err := c.mq.Publish(message.Context(), c.config.DLQTopic, message.Data(), mq.WithHeaders(headers)); err != nil {
		_ = message.Nak()
		return fmt.Errorf("publish agent stream DLQ: %w", err)
	}
	return message.Ack()
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
	close(c.jobs)
	c.wg.Wait()
	c.cancel()
	return result
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	return fmt.Errorf("%v; ack: %w", left, right)
}
