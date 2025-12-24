package connection

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/gateway/client"
)

// PresenceCallback 上下线回调函数
type PresenceCallback struct {
	logicClient *client.Client
	logger      clog.Logger
}

// NewPresenceCallback 创建上下线回调
// 用于在用户上线/下线时通知 Logic 服务
func NewPresenceCallback(logicClient *client.Client, logger clog.Logger) *PresenceCallback {
	return &PresenceCallback{
		logicClient: logicClient,
		logger:      logger,
	}
}

// OnUserOnline 用户上线回调
func (p *PresenceCallback) OnUserOnline(username string, remoteIP string) error {
	ctx := context.Background()
	err := p.logicClient.SyncUserOnline(ctx, username, remoteIP)
	if err != nil {
		p.logger.Error("failed to sync user online",
			clog.String("username", username),
			clog.String("remote_ip", remoteIP),
			clog.Error(err))
		return err
	}
	p.logger.Info("user online synced",
		clog.String("username", username),
		clog.String("remote_ip", remoteIP))
	return nil
}

// OnUserOffline 用户下线回调
func (p *PresenceCallback) OnUserOffline(username string) error {
	ctx := context.Background()
	err := p.logicClient.SyncUserOffline(ctx, username)
	if err != nil {
		p.logger.Error("failed to sync user offline",
			clog.String("username", username),
			clog.Error(err))
		return err
	}
	p.logger.Info("user offline synced",
		clog.String("username", username))
	return nil
}
