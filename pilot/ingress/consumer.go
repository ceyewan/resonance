package ingress

import (
	"context"
	"fmt"
	"sync"

	"github.com/ceyewan/genesis/mq"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
)

type ConsumerConfig struct {
	Topic       string
	QueueGroup  string
	DLQTopic    string
	MaxInflight int
}

type Consumer struct {
	config  ConsumerConfig
	mq      mq.MQ
	handler *Handler

	ctx          context.Context
	cancel       context.CancelFunc
	subscription mq.Subscription

	mu       sync.Mutex
	stopping bool
	inflight sync.WaitGroup
}

func NewConsumer(config ConsumerConfig, mqClient mq.MQ, handler *Handler) (*Consumer, error) {
	if config.Topic == "" || config.QueueGroup == "" || config.DLQTopic == "" || config.MaxInflight < 1 || mqClient == nil || handler == nil {
		return nil, fmt.Errorf("agent ingress consumer configuration is incomplete")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Consumer{config: config, mq: mqClient, handler: handler, ctx: ctx, cancel: cancel}, nil
}

func (c *Consumer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscription != nil || c.stopping {
		return fmt.Errorf("agent ingress consumer is already started or stopped")
	}
	subscription, err := c.mq.Subscribe(c.ctx, c.config.Topic, c.handleMessage,
		mq.WithQueueGroup(c.config.QueueGroup),
		mq.WithManualAck(),
		mq.WithMaxInflight(c.config.MaxInflight),
	)
	if err != nil {
		return fmt.Errorf("subscribe agent ingress: %w", err)
	}
	c.subscription = subscription
	return nil
}

func (c *Consumer) handleMessage(message mq.Message) error {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		_ = message.Nak()
		return context.Canceled
	}
	c.inflight.Add(1)
	c.mu.Unlock()
	defer c.inflight.Done()

	envelope := &mqv1.MQEvent{}
	if err := proto.Unmarshal(message.Data(), envelope); err != nil {
		return c.deadLetterAndAck(message, "malformed_protobuf")
	}
	_, err := c.handler.Handle(message.Context(), envelope)
	if err == nil {
		return message.Ack()
	}
	if IsPermanent(err) {
		return c.deadLetterAndAck(message, "permanent_rejection")
	}
	if nakErr := message.Nak(); nakErr != nil {
		return fmt.Errorf("handle agent event: %v; nak: %w", err, nakErr)
	}
	return err
}

func (c *Consumer) deadLetterAndAck(message mq.Message, reason string) error {
	headers := message.Headers()
	if headers == nil {
		headers = make(mq.Headers)
	}
	headers.Set("x-original-topic", message.Topic())
	headers.Set("x-agent-ingress-reason", reason)
	if err := c.mq.Publish(message.Context(), c.config.DLQTopic, message.Data(), mq.WithHeaders(headers)); err != nil {
		_ = message.Nak()
		return fmt.Errorf("publish agent ingress DLQ: %w", err)
	}
	return message.Ack()
}

func (c *Consumer) Stop() error {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		c.inflight.Wait()
		return nil
	}
	c.stopping = true
	subscription := c.subscription
	c.mu.Unlock()

	var unsubscribeErr error
	if subscription != nil {
		unsubscribeErr = subscription.Unsubscribe()
	}
	c.cancel()
	c.inflight.Wait()
	return unsubscribeErr
}
