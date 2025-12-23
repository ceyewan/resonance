package pusher

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
)

// GatewayPusher Gateway 推送客户端
type GatewayPusher struct {
	connMgr *ConnectionManager
	logger  clog.Logger
}

// NewGatewayPusher 创建 Gateway 推送器
func NewGatewayPusher(reg registry.Registry, gatewayServiceName string, logger clog.Logger) (*GatewayPusher, error) {
	if reg == nil {
		return nil, ErrGatewayNotFound
	}

	return &GatewayPusher{
		connMgr: NewConnectionManager(reg, gatewayServiceName, logger),
		logger:  logger.WithNamespace("pusher"),
	}, nil
}

// Push 推送消息到指定 Gateway
func (p *GatewayPusher) Push(ctx context.Context, gatewayID, username string, msg *gatewayv1.PushMessage) error {
	return p.connMgr.Push(ctx, gatewayID, username, msg)
}

// Close 关闭所有连接
func (p *GatewayPusher) Close() error {
	return p.connMgr.Close()
}
