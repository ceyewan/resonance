package service

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
)

// PresenceService 处理网关状态同步相关的请求
type PresenceService struct {
	logicv1.UnimplementedPresenceServiceServer
	routerRepo repo.RouterRepo
	logger     clog.Logger
}

// NewPresenceService 创建网关操作服务
func NewPresenceService(
	routerRepo repo.RouterRepo,
	logger clog.Logger,
) *PresenceService {
	return &PresenceService{
		routerRepo: routerRepo,
		logger:     logger,
	}
}

// SyncStatus 实现 PresenceService.SyncStatus（Unary 调用，支持批量）
func (s *PresenceService) SyncStatus(ctx context.Context, req *logicv1.SyncStatusRequest) (*logicv1.SyncStatusResponse, error) {
	s.logger.Debug("syncing status",
		clog.String("gateway_id", req.GatewayId),
		clog.Int("online_count", len(req.OnlineBatch)),
		clog.Int("offline_count", len(req.OfflineBatch)))

	// 1. 处理上线列表
	for _, online := range req.OnlineBatch {
		if err := s.handleUserOnline(ctx, req.GatewayId, online); err != nil {
			s.logger.Error("failed to handle user online",
				clog.String("username", online.Username),
				clog.Error(err))
			// 继续处理其他用户，记录错误但不中断
		}
	}

	// 2. 处理下线列表
	for _, offline := range req.OfflineBatch {
		if err := s.handleUserOffline(ctx, req.GatewayId, offline); err != nil {
			s.logger.Error("failed to handle user offline",
				clog.String("username", offline.Username),
				clog.Error(err))
		}
	}

	return &logicv1.SyncStatusResponse{
		SeqId: req.SeqId,
		Error: "", // 这里简单返回空，实际可以聚合错误信息
	}, nil
}

// handleUserOnline 处理单个用户上线
func (s *PresenceService) handleUserOnline(
	ctx context.Context,
	gatewayID string,
	event *logicv1.UserOnline,
) error {
	s.logger.Debug("user online",
		clog.String("username", event.Username),
		clog.String("gateway_id", gatewayID),
		clog.String("remote_ip", event.RemoteIp))

	// 保存用户路由信息到 RouterRepo
	router := &model.Router{
		Username:  event.Username,
		GatewayID: gatewayID,
		RemoteIP:  event.RemoteIp,
		Timestamp: event.Timestamp,
	}

	return s.routerRepo.SetUserGateway(ctx, router)
}

// handleUserOffline 处理单个用户下线
func (s *PresenceService) handleUserOffline(
	ctx context.Context,
	gatewayID string,
	event *logicv1.UserOffline,
) error {
	s.logger.Debug("user offline",
		clog.String("username", event.Username),
		clog.String("gateway_id", gatewayID))

	// 删除用户路由信息
	return s.routerRepo.DeleteUserGateway(ctx, event.Username)
}

// IsUserOnline 检查用户是否在线
func (s *PresenceService) IsUserOnline(ctx context.Context, username string) (bool, string, error) {
	router, err := s.routerRepo.GetUserGateway(ctx, username)
	if err != nil {
		return false, "", err
	}
	if router == nil {
		return false, "", nil
	}
	return true, router.GatewayID, nil
}
