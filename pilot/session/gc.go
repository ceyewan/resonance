package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ReferenceLocker must hold a cluster-wide lock while it snapshots all live
// object references and invokes prune. Returning acquired=false is a normal
// outcome when another Pilot instance owns the maintenance lease.
type ReferenceLocker interface {
	WithAgentSessionGCLock(
		ctx context.Context,
		lockID string,
		prune func(context.Context, []string) error,
	) (acquired bool, err error)
}

type ObjectPruner interface {
	PruneObjects(context.Context, []string, time.Time) (PruneResult, error)
}

type GCConfig struct {
	LockID   string
	Interval time.Duration
	Grace    time.Duration
}

type GarbageCollector struct {
	config GCConfig
	source ReferenceLocker
	store  ObjectPruner
	now    func() time.Time

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
	errors  chan error
}

func NewGarbageCollector(config GCConfig, source ReferenceLocker, store ObjectPruner) (*GarbageCollector, error) {
	if config.LockID == "" || config.Interval <= 0 || config.Grace <= 0 || source == nil || store == nil {
		return nil, fmt.Errorf("session garbage collector configuration is incomplete")
	}
	return &GarbageCollector{
		config: config, source: source, store: store, now: time.Now,
		done: make(chan struct{}), errors: make(chan error, 1),
	}, nil
}

func (c *GarbageCollector) Start(parent context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return fmt.Errorf("session garbage collector already started")
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.started = true
	go c.loop(ctx)
	return nil
}

func (c *GarbageCollector) loop(ctx context.Context) {
	defer close(c.done)
	c.collectAndReport(ctx)
	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collectAndReport(ctx)
		}
	}
}

func (c *GarbageCollector) collectAndReport(ctx context.Context) {
	_, err := c.CollectOnce(ctx)
	if err == nil || ctx.Err() != nil {
		return
	}
	select {
	case c.errors <- err:
	default:
	}
}

func (c *GarbageCollector) CollectOnce(ctx context.Context) (PruneResult, error) {
	var result PruneResult
	acquired, err := c.source.WithAgentSessionGCLock(
		ctx, c.config.LockID,
		func(lockContext context.Context, references []string) error {
			var pruneErr error
			result, pruneErr = c.store.PruneObjects(lockContext, references, c.now().UTC().Add(-c.config.Grace))
			return pruneErr
		},
	)
	if err != nil {
		return result, fmt.Errorf("session garbage collection: %w", err)
	}
	if !acquired {
		return PruneResult{}, nil
	}
	return result, nil
}

func (c *GarbageCollector) Errors() <-chan error { return c.errors }

func (c *GarbageCollector) Stop() {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	cancel()
	<-done
}

func (m *LocalManager) GCLockID() string {
	return "session-gc:" + shortDigest(m.root)
}
