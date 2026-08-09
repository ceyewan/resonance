package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/ceyewan/resonance/task/pusher"
)

type fakeHealthServer struct {
	startErr    error
	stopErr     error
	startCalled bool
	stopCalled  bool
	readyValues []bool
	calls       *[]string
}

func (h *fakeHealthServer) Start() error {
	h.startCalled = true
	if h.calls != nil {
		*h.calls = append(*h.calls, "health_start")
	}
	return h.startErr
}

func (h *fakeHealthServer) Stop(ctx context.Context) error {
	h.stopCalled = true
	if h.calls != nil {
		*h.calls = append(*h.calls, "health_stop")
	}
	return h.stopErr
}

func (h *fakeHealthServer) SetReady(ready bool) {
	h.readyValues = append(h.readyValues, ready)
	if h.calls != nil {
		if ready {
			*h.calls = append(*h.calls, "health_ready_true")
		} else {
			*h.calls = append(*h.calls, "health_ready_false")
		}
	}
}

type fakePusherManager struct {
	startErr    error
	startCalled bool
	closeCalled bool
	calls       *[]string
}

func (m *fakePusherManager) Start() error {
	m.startCalled = true
	if m.calls != nil {
		*m.calls = append(*m.calls, "pusher_start")
	}
	return m.startErr
}

func (m *fakePusherManager) GetClient(gatewayID string) (pusher.Client, error) {
	return nil, errors.New("not used")
}

func (m *fakePusherManager) Close() {
	m.closeCalled = true
	if m.calls != nil {
		*m.calls = append(*m.calls, "pusher_close")
	}
}

type fakeConsumer struct {
	startErr    error
	stopErr     error
	startCalled bool
	stopCalled  bool
	setNames    []string
	calls       *[]string
}

func (c *fakeConsumer) Start() error {
	c.startCalled = true
	if c.calls != nil {
		*c.calls = append(*c.calls, "consumer_start")
	}
	return c.startErr
}

func (c *fakeConsumer) Stop() error {
	c.stopCalled = true
	if c.calls != nil {
		*c.calls = append(*c.calls, "consumer_stop")
	}
	return c.stopErr
}

func (c *fakeConsumer) SetName(name string) {
	c.setNames = append(c.setNames, name)
}

type fakeCloser struct {
	name       string
	closeErr   error
	closeCalls int
	calls      *[]string
}

func (c *fakeCloser) Close() error {
	c.closeCalls++
	if c.calls != nil {
		*c.calls = append(*c.calls, c.name+"_close")
	}
	return c.closeErr
}

type fakeRegistryCloser struct {
	fakeCloser
}

func (r *fakeRegistryCloser) Register(ctx context.Context, service *registry.ServiceInstance, ttl time.Duration) error {
	return nil
}

func (r *fakeRegistryCloser) Deregister(ctx context.Context, serviceID string) error {
	return nil
}

func (r *fakeRegistryCloser) GetService(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (r *fakeRegistryCloser) Watch(ctx context.Context, serviceName string) (<-chan registry.ServiceEvent, error) {
	return nil, nil
}

func (r *fakeRegistryCloser) GetConnection(ctx context.Context, serviceName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return nil, nil
}

func newTestTask() *Task {
	ctx, cancel := context.WithCancel(context.Background())
	return &Task{
		logger: clog.Discard(),
		ctx:    ctx,
		cancel: cancel,
	}
}

func TestTask_Run_HealthStartFailed(t *testing.T) {
	task := newTestTask()
	h := &fakeHealthServer{startErr: errors.New("listen failed")}
	p := &fakePusherManager{}
	c := &fakeConsumer{}

	task.healthServer = h
	task.pusherMgr = p
	task.consumer = c

	err := task.Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "health server start")
	require.True(t, h.startCalled)
	require.False(t, p.startCalled)
	require.False(t, c.startCalled)
}

func TestTask_Run_PusherStartFailed(t *testing.T) {
	task := newTestTask()
	h := &fakeHealthServer{}
	p := &fakePusherManager{startErr: errors.New("registry down")}
	c := &fakeConsumer{}

	task.healthServer = h
	task.pusherMgr = p
	task.consumer = c

	err := task.Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pusher manager start")
	require.True(t, h.startCalled)
	require.True(t, p.startCalled)
	require.False(t, c.startCalled)
}

func TestTask_Run_ConsumerStartFailed(t *testing.T) {
	task := newTestTask()
	h := &fakeHealthServer{}
	p := &fakePusherManager{}
	c := &fakeConsumer{startErr: errors.New("subscribe failed")}

	task.healthServer = h
	task.pusherMgr = p
	task.consumer = c

	err := task.Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "consumer start")
	require.True(t, h.startCalled)
	require.True(t, p.startCalled)
	require.True(t, c.startCalled)
}

func TestTask_Run_Success(t *testing.T) {
	task := newTestTask()
	h := &fakeHealthServer{}
	p := &fakePusherManager{}
	c := &fakeConsumer{}

	task.healthServer = h
	task.pusherMgr = p
	task.consumer = c

	err := task.Run()
	require.NoError(t, err)
	require.True(t, h.startCalled)
	require.True(t, p.startCalled)
	require.True(t, c.startCalled)
	require.Equal(t, []bool{true}, h.readyValues)
}

func TestTask_Close_OrderAndResourceClose(t *testing.T) {
	task := newTestTask()
	calls := make([]string, 0, 16)

	task.healthServer = &fakeHealthServer{calls: &calls}
	task.consumer = &fakeConsumer{calls: &calls}
	task.pusherMgr = &fakePusherManager{calls: &calls}
	task.resources = &resources{
		registry:     &fakeRegistryCloser{fakeCloser: fakeCloser{name: "registry", calls: &calls}},
		etcdConn:     &fakeCloser{name: "etcd", calls: &calls},
		natsConn:     &fakeCloser{name: "nats", calls: &calls},
		redisConn:    &fakeCloser{name: "redis", calls: &calls},
		postgresConn: &fakeCloser{name: "postgres", calls: &calls},
	}

	err := task.Close()
	require.NoError(t, err)
	require.Equal(t, []string{
		"health_ready_false",
		"health_stop",
		"consumer_stop",
		"pusher_close",
		"registry_close",
		"etcd_close",
		"nats_close",
		"redis_close",
		"postgres_close",
	}, calls)
}

func TestTask_Close_IgnoreConsumerStopError(t *testing.T) {
	task := newTestTask()
	task.healthServer = &fakeHealthServer{}
	task.consumer = &fakeConsumer{stopErr: errors.New("stop failed")}
	task.pusherMgr = &fakePusherManager{}

	err := task.Close()
	require.NoError(t, err)
}
