package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

type Processor interface {
	ProcessOne(ctx context.Context) (*model.AgentRun, error)
	Recover(ctx context.Context) (repo.AgentRunRecoveryResult, error)
}

type WorkerConfig struct {
	WorkerCount      int
	PollInterval     time.Duration
	RecoveryInterval time.Duration
}

type WorkerPool struct {
	config    WorkerConfig
	processor Processor

	mu           sync.Mutex
	started      bool
	claimCancel  context.CancelFunc
	activeCancel context.CancelFunc
	wg           sync.WaitGroup
	errors       chan error
}

func NewWorkerPool(config WorkerConfig, processor Processor) (*WorkerPool, error) {
	if config.WorkerCount < 1 || config.PollInterval <= 0 || config.RecoveryInterval <= 0 || processor == nil {
		return nil, fmt.Errorf("agent worker pool configuration is invalid")
	}
	return &WorkerPool{config: config, processor: processor, errors: make(chan error, config.WorkerCount+1)}, nil
}

func (p *WorkerPool) Start(parent context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return fmt.Errorf("agent worker pool already started")
	}
	activeContext, activeCancel := context.WithCancel(parent)
	claimContext, claimCancel := context.WithCancel(context.Background())
	p.activeCancel = activeCancel
	p.claimCancel = claimCancel
	p.started = true

	if _, err := p.processor.Recover(activeContext); err != nil {
		p.started = false
		claimCancel()
		activeCancel()
		return fmt.Errorf("initial agent run recovery: %w", err)
	}
	for worker := 0; worker < p.config.WorkerCount; worker++ {
		p.wg.Add(1)
		go p.worker(activeContext, claimContext)
	}
	p.wg.Add(1)
	go p.reconciler(activeContext, claimContext)
	return nil
}

func (p *WorkerPool) worker(activeContext, claimContext context.Context) {
	defer p.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-activeContext.Done():
			return
		case <-claimContext.Done():
			return
		case <-timer.C:
		}
		select {
		case <-activeContext.Done():
			return
		case <-claimContext.Done():
			return
		default:
		}

		_, err := p.processor.ProcessOne(activeContext)
		switch {
		case err == nil:
			timer.Reset(0)
		case errors.Is(err, ErrNoWork):
			timer.Reset(p.config.PollInterval)
		case errors.Is(err, context.Canceled) && activeContext.Err() != nil:
			return
		default:
			p.report(fmt.Errorf("agent worker process: %w", err))
			timer.Reset(p.config.PollInterval)
		}
	}
}

func (p *WorkerPool) reconciler(activeContext, claimContext context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.config.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-activeContext.Done():
			return
		case <-claimContext.Done():
			return
		case <-ticker.C:
			if _, err := p.processor.Recover(activeContext); err != nil {
				p.report(fmt.Errorf("agent run recovery: %w", err))
			}
		}
	}
}

func (p *WorkerPool) StopClaiming() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claimCancel != nil {
		p.claimCancel()
	}
}

// Drain 等待当前 Run 自然完成；它不会取消 Runtime。
func (p *WorkerPool) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AbortActive 只用于 drain 超时；Runtime 会通过执行 context 走显式 Abort 状态机。
func (p *WorkerPool) AbortActive() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeCancel != nil {
		p.activeCancel()
	}
}

func (p *WorkerPool) Errors() <-chan error { return p.errors }

func (p *WorkerPool) report(err error) {
	select {
	case p.errors <- err:
	default:
	}
}
