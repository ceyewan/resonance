package pilot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/pilot/config"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestPilot_LogicClientDoesNotTransparentlyRetrySignedCalls(t *testing.T) {
	require.NotContains(t, logicClientServiceConfig, "retryPolicy")
}

func TestPilot_RunAndClosePreserveDependencyOrder(t *testing.T) {
	recorder := &pilotRecorder{}
	p := newLifecyclePilot(t, recorder)
	require.NoError(t, p.Run())
	require.Equal(t, []string{"health.start", "runtime.probe", "broker.start", "workers.start", "ingress.start", "health.ready:true"}, recorder.snapshot())

	recorder.reset()
	require.NoError(t, p.Close())
	require.NoError(t, p.Close(), "Close must be idempotent")
	require.Equal(t, []string{
		"health.ready:false", "ingress.stop", "workers.stop_claiming", "workers.drain",
		"runtime.shutdown", "broker.close", "resources.close", "health.stop",
	}, recorder.snapshot())
}

func TestPilot_DrainTimeoutAbortsOnlyAfterStoppingClaims(t *testing.T) {
	recorder := &pilotRecorder{}
	p := newLifecyclePilot(t, recorder)
	p.config.Worker.ShutdownDrainTimeout = 10 * time.Millisecond
	p.workers.(*fakeWorkers).blockDrain = true
	require.NoError(t, p.Run())
	recorder.reset()
	require.NoError(t, p.Close())
	calls := recorder.snapshot()
	require.Less(t, indexOf(calls, "workers.stop_claiming"), indexOf(calls, "workers.abort"))
	require.Less(t, indexOf(calls, "workers.abort"), indexOf(calls, "runtime.shutdown"))
}

func TestPilot_RunFailureRollsBackStartedComponents(t *testing.T) {
	recorder := &pilotRecorder{}
	p := newLifecyclePilot(t, recorder)
	p.broker.(*fakeBroker).startErr = errors.New("listen failed")
	err := p.Run()
	require.ErrorContains(t, err, "listen failed")
	require.Equal(t, []string{
		"health.start", "runtime.probe", "broker.start", "health.ready:false",
		"runtime.shutdown", "health.stop",
	}, recorder.snapshot())
}

func TestPilot_MaintenanceRunsAfterProbeAndStopsAfterRuntimeDrain(t *testing.T) {
	recorder := &pilotRecorder{}
	p := newLifecyclePilot(t, recorder)
	p.maintenance = &fakeMaintenance{recorder: recorder, errors: make(chan error, 1)}
	require.NoError(t, p.Run())
	require.Equal(t, []string{
		"health.start", "runtime.probe", "maintenance.start", "broker.start",
		"workers.start", "ingress.start", "health.ready:true",
	}, recorder.snapshot())

	recorder.reset()
	require.NoError(t, p.Close())
	require.Equal(t, []string{
		"health.ready:false", "ingress.stop", "workers.stop_claiming", "workers.drain",
		"runtime.shutdown", "maintenance.stop", "broker.close", "resources.close", "health.stop",
	}, recorder.snapshot())
}

func TestPilot_IAMMutationReconcilerStartsBeforeBrokerAndStopsAfterRuntimeDrain(t *testing.T) {
	recorder := &pilotRecorder{}
	p := newLifecyclePilot(t, recorder)
	p.mutations = &fakeMutations{recorder: recorder, errors: make(chan error, 1)}
	require.NoError(t, p.Run())
	require.Equal(t, []string{
		"health.start", "runtime.probe", "mutations.start", "broker.start",
		"workers.start", "ingress.start", "health.ready:true",
	}, recorder.snapshot())

	recorder.reset()
	require.NoError(t, p.Close())
	require.Equal(t, []string{
		"health.ready:false", "ingress.stop", "workers.stop_claiming", "workers.drain",
		"runtime.shutdown", "mutations.stop", "broker.close", "resources.close", "health.stop",
	}, recorder.snapshot())
}

func TestPilot_BrokerFatalClearsReadinessAndSignalsMain(t *testing.T) {
	recorder := &pilotRecorder{}
	p := newLifecyclePilot(t, recorder)
	require.NoError(t, p.Run())
	p.broker.(*fakeBroker).errors <- errors.New("broker failed")
	select {
	case err := <-p.Errors():
		require.ErrorContains(t, err, "broker failed")
	case <-time.After(time.Second):
		t.Fatal("fatal broker failure was not reported")
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("fatal broker failure did not cancel Pilot")
	}
	require.Contains(t, recorder.snapshot(), "health.ready:false")
	require.NoError(t, p.Close())
}

func newLifecyclePilot(t *testing.T, recorder *pilotRecorder) *Pilot {
	t.Helper()
	cfg := &config.Config{}
	cfg.Worker.ShutdownDrainTimeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	return &Pilot{
		config: cfg, logger: clog.Discard(), ingress: &fakeIngress{recorder: recorder},
		workers: &fakeWorkers{recorder: recorder, errors: make(chan error, 1)},
		runtime: &fakeRuntime{recorder: recorder}, broker: &fakeBroker{recorder: recorder, errors: make(chan error, 1)},
		health: &fakeHealth{recorder: recorder}, closeResources: func() error { recorder.add("resources.close"); return nil },
		ctx: ctx, cancel: cancel, errors: make(chan error, 1),
	}
}

type pilotRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *pilotRecorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}
func (r *pilotRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}
func (r *pilotRecorder) reset() { r.mu.Lock(); defer r.mu.Unlock(); r.calls = nil }

type fakeIngress struct{ recorder *pilotRecorder }

func (f *fakeIngress) Start() error { f.recorder.add("ingress.start"); return nil }
func (f *fakeIngress) Stop() error  { f.recorder.add("ingress.stop"); return nil }

type fakeWorkers struct {
	recorder   *pilotRecorder
	errors     chan error
	blockDrain bool
	aborted    bool
}

func (f *fakeWorkers) Start(context.Context) error { f.recorder.add("workers.start"); return nil }
func (f *fakeWorkers) StopClaiming()               { f.recorder.add("workers.stop_claiming") }
func (f *fakeWorkers) Drain(ctx context.Context) error {
	f.recorder.add("workers.drain")
	if !f.blockDrain || f.aborted {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeWorkers) AbortActive()         { f.recorder.add("workers.abort"); f.aborted = true }
func (f *fakeWorkers) Errors() <-chan error { return f.errors }

type fakeRuntime struct{ recorder *pilotRecorder }

func (f *fakeRuntime) Run(context.Context, pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
	return nil, errors.New("unused")
}
func (f *fakeRuntime) Abort(context.Context, string) error { return nil }
func (f *fakeRuntime) Probe(context.Context) error         { f.recorder.add("runtime.probe"); return nil }
func (f *fakeRuntime) Shutdown(context.Context) error      { f.recorder.add("runtime.shutdown"); return nil }

type fakeBroker struct {
	recorder *pilotRecorder
	errors   chan error
	startErr error
}

func (f *fakeBroker) Start() error                { f.recorder.add("broker.start"); return f.startErr }
func (f *fakeBroker) Close(context.Context) error { f.recorder.add("broker.close"); return nil }
func (f *fakeBroker) Errors() <-chan error        { return f.errors }

type fakeHealth struct{ recorder *pilotRecorder }

func (f *fakeHealth) Start() error               { f.recorder.add("health.start"); return nil }
func (f *fakeHealth) Stop(context.Context) error { f.recorder.add("health.stop"); return nil }
func (f *fakeHealth) SetReady(ready bool) {
	f.recorder.add("health.ready:" + map[bool]string{true: "true", false: "false"}[ready])
}

type fakeMaintenance struct {
	recorder *pilotRecorder
	errors   chan error
}

type fakeMutations struct {
	recorder *pilotRecorder
	errors   chan error
}

func (f *fakeMutations) Start(context.Context) error {
	f.recorder.add("mutations.start")
	return nil
}
func (f *fakeMutations) Stop() error          { f.recorder.add("mutations.stop"); return nil }
func (f *fakeMutations) Errors() <-chan error { return f.errors }

func (f *fakeMaintenance) Start(context.Context) error {
	f.recorder.add("maintenance.start")
	return nil
}
func (f *fakeMaintenance) Stop()                { f.recorder.add("maintenance.stop") }
func (f *fakeMaintenance) Errors() <-chan error { return f.errors }

func indexOf(values []string, expected string) int {
	for index, value := range values {
		if value == expected {
			return index
		}
	}
	return -1
}
