package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGarbageCollector_HoldsReferenceLockAcrossPrune(t *testing.T) {
	locker := &testReferenceLocker{references: []string{"objects/aa/" + sixtyFourA + ".jsonl"}, acquired: true}
	pruner := &testObjectPruner{result: PruneResult{Deleted: 2}}
	collector, err := NewGarbageCollector(GCConfig{
		LockID: "session-gc:test", Interval: time.Hour, Grace: 24 * time.Hour,
	}, locker, pruner)
	require.NoError(t, err)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	collector.now = func() time.Time { return now }

	result, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, result.Deleted)
	require.True(t, locker.inCallback)
	require.Equal(t, now.Add(-24*time.Hour), pruner.cutoff)
	require.Equal(t, locker.references, pruner.references)
}

func TestGarbageCollector_LockContentionIsNormalAndLifecycleStops(t *testing.T) {
	locker := &testReferenceLocker{acquired: false}
	pruner := &testObjectPruner{}
	collector, err := NewGarbageCollector(GCConfig{
		LockID: "session-gc:test", Interval: time.Millisecond, Grace: time.Hour,
	}, locker, pruner)
	require.NoError(t, err)
	require.NoError(t, collector.Start(context.Background()))
	require.Error(t, collector.Start(context.Background()))
	require.Eventually(t, func() bool { return locker.calls() > 0 }, time.Second, 10*time.Millisecond)
	collector.Stop()
	collector.Stop()
	require.Zero(t, pruner.calls)
}

func TestGarbageCollector_ReportsBoundedBackgroundErrors(t *testing.T) {
	locker := &testReferenceLocker{acquired: true, err: errors.New("database unavailable")}
	collector, err := NewGarbageCollector(GCConfig{
		LockID: "session-gc:test", Interval: time.Hour, Grace: time.Hour,
	}, locker, &testObjectPruner{})
	require.NoError(t, err)
	require.NoError(t, collector.Start(context.Background()))
	select {
	case reported := <-collector.Errors():
		require.ErrorContains(t, reported, "database unavailable")
	case <-time.After(time.Second):
		t.Fatal("garbage collector did not report its maintenance error")
	}
	collector.Stop()
}

const sixtyFourA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type testReferenceLocker struct {
	mu         sync.Mutex
	references []string
	acquired   bool
	err        error
	count      int
	inCallback bool
}

func (l *testReferenceLocker) WithAgentSessionGCLock(
	ctx context.Context,
	_ string,
	prune func(context.Context, []string) error,
) (bool, error) {
	l.mu.Lock()
	l.count++
	l.mu.Unlock()
	if l.err != nil || !l.acquired {
		return l.acquired, l.err
	}
	l.mu.Lock()
	l.inCallback = true
	l.mu.Unlock()
	err := prune(ctx, append([]string(nil), l.references...))
	return true, err
}

func (l *testReferenceLocker) calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

type testObjectPruner struct {
	result     PruneResult
	err        error
	references []string
	cutoff     time.Time
	calls      int
}

func (p *testObjectPruner) PruneObjects(_ context.Context, references []string, cutoff time.Time) (PruneResult, error) {
	p.calls++
	p.references = append([]string(nil), references...)
	p.cutoff = cutoff
	return p.result, p.err
}
