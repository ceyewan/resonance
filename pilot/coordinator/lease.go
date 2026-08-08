package coordinator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

type leaseGuard struct {
	store    RunStore
	duration time.Duration
	interval time.Duration
	now      func() time.Time

	mu   sync.Mutex
	run  *model.AgentRun
	lost error

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func newLeaseGuard(store RunStore, run *model.AgentRun, duration, interval time.Duration, now func() time.Time) *leaseGuard {
	return &leaseGuard{
		store: store, duration: duration, interval: interval, now: now,
		run: cloneAgentRun(run), stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
}

func (g *leaseGuard) start(cancel context.CancelFunc) {
	go func() {
		defer close(g.doneCh)
		ticker := time.NewTicker(g.interval)
		defer ticker.Stop()
		for {
			select {
			case <-g.stopCh:
				return
			case <-ticker.C:
				g.mu.Lock()
				current := cloneAgentRun(g.run)
				if g.lost != nil {
					g.mu.Unlock()
					return
				}
				now := g.now().UTC()
				heartbeatContext, heartbeatCancel := context.WithTimeout(context.Background(), g.interval)
				next, err := g.store.HeartbeatAgentRun(heartbeatContext, repo.AgentRunLease{
					TenantID: current.TenantID, RunID: current.RunID,
					WorkerID: current.LeaseOwner, LeaseToken: current.LeaseToken,
					ExpectedVersion: current.Version, Now: now, LeaseDuration: g.duration,
				})
				heartbeatCancel()
				if err != nil {
					g.lost = err
					g.mu.Unlock()
					cancel()
					return
				}
				g.run = cloneAgentRun(next)
				g.mu.Unlock()
			}
		}
	}()
}

func (g *leaseGuard) stop() {
	g.stopOnce.Do(func() { close(g.stopCh) })
	<-g.doneCh
}

func (g *leaseGuard) snapshot() *model.AgentRun {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cloneAgentRun(g.run)
}

func (g *leaseGuard) lostError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lost
}

func (g *leaseGuard) apply(operation func(current *model.AgentRun, now time.Time) (*model.AgentRun, error)) (*model.AgentRun, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lost != nil {
		return nil, g.lost
	}
	next, err := operation(cloneAgentRun(g.run), g.now().UTC())
	if err != nil {
		if errors.Is(err, repo.ErrAgentRunFenceLost) {
			g.lost = err
		}
		return nil, err
	}
	g.run = cloneAgentRun(next)
	return cloneAgentRun(next), nil
}

func (g *leaseGuard) lease(current *model.AgentRun, now time.Time) repo.AgentRunLease {
	return repo.AgentRunLease{
		TenantID: current.TenantID, RunID: current.RunID,
		WorkerID: current.LeaseOwner, LeaseToken: current.LeaseToken,
		ExpectedVersion: current.Version, Now: now, LeaseDuration: g.duration,
	}
}

func cloneAgentRun(run *model.AgentRun) *model.AgentRun {
	if run == nil {
		return nil
	}
	clone := *run
	return &clone
}
