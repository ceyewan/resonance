package pusher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type testRegistry struct {
	getServiceFn func(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error)
}

func (r *testRegistry) Register(ctx context.Context, service *registry.ServiceInstance, ttl time.Duration) error {
	return nil
}

func (r *testRegistry) Deregister(ctx context.Context, serviceID string) error {
	return nil
}

func (r *testRegistry) GetService(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
	if r.getServiceFn != nil {
		return r.getServiceFn(ctx, serviceName)
	}
	return nil, nil
}

func (r *testRegistry) Watch(ctx context.Context, serviceName string) (<-chan registry.ServiceEvent, error) {
	return nil, nil
}

func (r *testRegistry) LeaseFailures() <-chan registry.LeaseFailure { return nil }

func (r *testRegistry) GetConnection(ctx context.Context, serviceName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return nil, nil
}

func (r *testRegistry) Close() error {
	return nil
}

func (r *testRegistry) Shutdown(context.Context) error { return nil }

func TestManager_GetClient_NotFound(t *testing.T) {
	m := NewManager(clog.Discard(), &testRegistry{}, "gateway", 10, 1, time.Second)
	defer m.Close()

	_, err := m.GetClient("gw-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "gateway client not found")
}

func TestManager_syncServices_ServiceNotFoundIsNil(t *testing.T) {
	m := NewManager(clog.Discard(), &testRegistry{
		getServiceFn: func(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
			return nil, registry.ErrServiceNotFound
		},
	}, "gateway", 10, 1, time.Second)
	defer m.Close()

	err := m.syncServices()
	require.NoError(t, err)
}

func TestManager_syncServices_GetServiceFailed(t *testing.T) {
	m := NewManager(clog.Discard(), &testRegistry{
		getServiceFn: func(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
			return nil, errors.New("etcd down")
		},
	}, "gateway", 10, 1, time.Second)
	defer m.Close()

	err := m.syncServices()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get services")
}

func TestManager_addClient_EmptyEndpointsSkip(t *testing.T) {
	m := NewManager(clog.Discard(), &testRegistry{}, "gateway", 10, 1, time.Second)
	defer m.Close()

	m.addClient(&registry.ServiceInstance{
		ID:        "gw-1",
		Name:      "gateway",
		Endpoints: nil,
	})

	_, err := m.GetClient("gw-1")
	require.Error(t, err)
}

func TestManager_Start_PropagateSyncError(t *testing.T) {
	m := NewManager(clog.Discard(), &testRegistry{
		getServiceFn: func(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
			return nil, errors.New("registry unavailable")
		},
	}, "gateway", 10, 1, time.Second)
	defer m.Close()

	err := m.Start()
	require.Error(t, err)
}
