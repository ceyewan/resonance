package service

import (
	"context"
	"io"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/cache"
	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
)

// GatewayOpsService 网关操作服务
type GatewayOpsService struct {
	cache  cache.Cache
	logger clog.Logger
}

// NewGatewayOpsService 创建网关操作服务
func NewGatewayOpsService(
	cache cache.Cache,
	logger clog.Logger,
) *GatewayOpsService {
	return &GatewayOpsService{
		cache:  cache,
		logger: logger,
	}
}

// SyncState 实现 GatewayOpsService.SyncState（双向流）
func (s *GatewayOpsService) SyncState(
	ctx context.Context,
	stream *connect.BidiStream[logicv1.SyncStateRequest, logicv1.SyncStateResponse],
) error {
	s.logger.Info("gateway ops stream established")

	for {
		req, err := stream.Receive()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("gateway ops stream closed by client")
				return nil
			}
			s.logger.Error("failed to receive state sync", clog.Error(err))
			return err
		}

		// 处理状态同步
		resp := s.handleStateSync(ctx, req)

		// 发送响应
		if err := stream.Send(resp); err != nil {
			s.logger.Error("failed to send response", clog.Error(err))
			return err
		}
	}
}

// handleStateSync 处理状态同步
func (s *GatewayOpsService) handleStateSync(ctx context.Context, req *logicv1.SyncStateRequest) *logicv1.SyncStateResponse {
	switch event := req.Event.(type) {
	case *logicv1.SyncStateRequest_Online:
		return s.handleUserOnline(ctx, req.SeqId, req.GatewayId, event.Online)
	case *logicv1.SyncStateRequest_Offline:
		return s.handleUserOffline(ctx, req.SeqId, req.GatewayId, event.Offline)
	default:
		s.logger.Warn("unknown event type")
		return &logicv1.SyncStateResponse{
			SeqId: req.SeqId,
			Error: "unknown event type",
		}
	}
}

// handleUserOnline 处理用户上线
func (s *GatewayOpsService) handleUserOnline(
	ctx context.Context,
	seqID int64,
	gatewayID string,
	event *logicv1.UserOnline,
) *logicv1.SyncStateResponse {
	s.logger.Info("user online",
		clog.String("username", event.Username),
		clog.String("gateway_id", gatewayID),
		clog.String("remote_ip", event.RemoteIp))

	// 将用户在线状态存储到 Redis
	// Key: user:online:{username} -> {gateway_id}
	key := "user:online:" + event.Username
	if err := s.cache.Set(ctx, key, gatewayID, 0); err != nil {
		s.logger.Error("failed to set user online status", clog.Error(err))
		return &logicv1.SyncStateResponse{
			SeqId: seqID,
			Error: "failed to set online status",
		}
	}

	return &logicv1.SyncStateResponse{
		SeqId: seqID,
		Error: "",
	}
}

// handleUserOffline 处理用户下线
func (s *GatewayOpsService) handleUserOffline(
	ctx context.Context,
	seqID int64,
	gatewayID string,
	event *logicv1.UserOffline,
) *logicv1.SyncStateResponse {
	s.logger.Info("user offline",
		clog.String("username", event.Username),
		clog.String("gateway_id", gatewayID))

	// 删除用户在线状态
	key := "user:online:" + event.Username
	if err := s.cache.Del(ctx, key); err != nil {
		s.logger.Error("failed to delete user online status", clog.Error(err))
		return &logicv1.SyncStateResponse{
			SeqId: seqID,
			Error: "failed to delete online status",
		}
	}

	return &logicv1.SyncStateResponse{
		SeqId: seqID,
		Error: "",
	}
}

// IsUserOnline 检查用户是否在线
func (s *GatewayOpsService) IsUserOnline(ctx context.Context, username string) (bool, string, error) {
	key := "user:online:" + username
	gatewayID, err := s.cache.Get(ctx, key)
	if err != nil {
		return false, "", err
	}
	if gatewayID == "" {
		return false, "", nil
	}
	return true, gatewayID, nil
}

