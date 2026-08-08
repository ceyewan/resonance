package coordinator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestWorkerPool_StopClaimingDrainsCurrentRunWithoutCancellation(t *testing.T) {
	processor := &blockingProcessor{started: make(chan struct{}), release: make(chan struct{})}
	pool, err := NewWorkerPool(WorkerConfig{WorkerCount: 1, PollInterval: time.Millisecond, RecoveryInterval: time.Hour}, processor)
	require.NoError(t, err)
	require.NoError(t, pool.Start(context.Background()))
	<-processor.started
	pool.StopClaiming()

	drainContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, pool.Drain(drainContext), context.DeadlineExceeded)
	require.False(t, processor.cancelled.Load())

	close(processor.release)
	drainContext, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Drain(drainContext))
	require.Equal(t, int64(1), processor.calls.Load())
}

func TestWorkerPool_AbortActiveCancelsCurrentRunAfterDrainTimeout(t *testing.T) {
	processor := &blockingProcessor{started: make(chan struct{}), release: make(chan struct{})}
	pool, err := NewWorkerPool(WorkerConfig{WorkerCount: 1, PollInterval: time.Millisecond, RecoveryInterval: time.Hour}, processor)
	require.NoError(t, err)
	require.NoError(t, pool.Start(context.Background()))
	<-processor.started
	pool.StopClaiming()
	pool.AbortActive()

	drainContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Drain(drainContext))
	require.True(t, processor.cancelled.Load())
}

func TestWorkerPool_ReportsProcessingErrorsAndContinues(t *testing.T) {
	processor := &sequenceProcessor{errors: []error{errors.New("injected"), ErrNoWork, ErrNoWork}}
	pool, err := NewWorkerPool(WorkerConfig{WorkerCount: 1, PollInterval: time.Millisecond, RecoveryInterval: time.Hour}, processor)
	require.NoError(t, err)
	require.NoError(t, pool.Start(context.Background()))
	select {
	case reported := <-pool.Errors():
		require.Contains(t, reported.Error(), "injected")
	case <-time.After(time.Second):
		t.Fatal("expected worker error")
	}
	require.Eventually(t, func() bool { return processor.calls.Load() >= 2 }, time.Second, time.Millisecond)
	pool.StopClaiming()
	drainContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Drain(drainContext))
	require.GreaterOrEqual(t, processor.calls.Load(), int64(2))
}

type blockingProcessor struct {
	once      sync.Once
	started   chan struct{}
	release   chan struct{}
	calls     atomic.Int64
	cancelled atomic.Bool
}

func (p *blockingProcessor) ProcessOne(ctx context.Context) (*model.AgentRun, error) {
	p.calls.Add(1)
	p.once.Do(func() { close(p.started) })
	select {
	case <-ctx.Done():
		p.cancelled.Store(true)
		return nil, ctx.Err()
	case <-p.release:
		return &model.AgentRun{}, nil
	}
}
func (*blockingProcessor) Recover(context.Context) (repo.AgentRunRecoveryResult, error) {
	return repo.AgentRunRecoveryResult{}, nil
}

type sequenceProcessor struct {
	mu     sync.Mutex
	errors []error
	calls  atomic.Int64
}

func (p *sequenceProcessor) ProcessOne(context.Context) (*model.AgentRun, error) {
	index := int(p.calls.Add(1) - 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < len(p.errors) {
		return nil, p.errors[index]
	}
	return nil, ErrNoWork
}
func (*sequenceProcessor) Recover(context.Context) (repo.AgentRunRecoveryResult, error) {
	return repo.AgentRunRecoveryResult{}, nil
}
