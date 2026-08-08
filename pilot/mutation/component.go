package mutation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/mq"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
)

type ComponentConfig struct {
	Topic       string
	QueueGroup  string
	MaxInflight int
}

// Component 消费审批决定事件，并用周期回查覆盖事件丢失与进程重启。
type Component struct {
	config  ComponentConfig
	mq      mq.MQ
	service *Service

	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	subscription mq.Subscription
	wg           sync.WaitGroup
	errors       chan error
}

func NewComponent(config ComponentConfig, mqClient mq.MQ, service *Service) (*Component, error) {
	if config.Topic == "" || config.QueueGroup == "" || config.MaxInflight < 1 || mqClient == nil || service == nil {
		return nil, fmt.Errorf("agent mutation component configuration is incomplete")
	}
	return &Component{config: config, mq: mqClient, service: service, errors: make(chan error, 8)}, nil
}

func (c *Component) Start(parent context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscription != nil || c.cancel != nil {
		return fmt.Errorf("agent mutation component is already started")
	}
	c.ctx, c.cancel = context.WithCancel(parent)
	subscription, err := c.mq.Subscribe(c.ctx, c.config.Topic, c.handleMessage,
		mq.WithQueueGroup(c.config.QueueGroup), mq.WithManualAck(), mq.WithMaxInflight(c.config.MaxInflight),
	)
	if err != nil {
		c.cancel()
		c.cancel = nil
		return fmt.Errorf("subscribe approval decisions: %w", err)
	}
	c.subscription = subscription
	c.wg.Add(1)
	go c.reconcileLoop()
	return nil
}

func (c *Component) handleMessage(message mq.Message) error {
	event := &mqv1.AgentApprovalDecidedEvent{}
	if err := proto.Unmarshal(message.Data(), event); err != nil || event.GetTenantId() == "" || event.GetCallId() == "" {
		return message.Ack()
	}
	// event 的 decision/args_hash/version 不是执行授权，只使用 tenant+call 唤醒回查。
	if event.GetTenantId() != c.service.config.TenantID {
		return message.Ack()
	}
	if err := c.service.ProcessCall(message.Context(), event.GetTenantId(), event.GetCallId()); err != nil {
		if nakErr := message.Nak(); nakErr != nil {
			return fmt.Errorf("process approval decision: %v; nak: %w", err, nakErr)
		}
		return err
	}
	return message.Ack()
}

func (c *Component) reconcileLoop() {
	defer c.wg.Done()
	c.reconcileOnce()
	ticker := time.NewTicker(c.service.config.ReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.reconcileOnce()
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Component) reconcileOnce() {
	if err := c.service.Reconcile(c.ctx); err != nil && c.ctx.Err() == nil {
		select {
		case c.errors <- err:
		default:
		}
	}
}

func (c *Component) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	subscription := c.subscription
	c.cancel = nil
	c.subscription = nil
	c.mu.Unlock()
	if subscription != nil {
		_ = subscription.Unsubscribe()
	}
	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
}

func (c *Component) Errors() <-chan error { return c.errors }
