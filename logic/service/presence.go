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

	// 1. 批量处理上线列表
	if len(req.OnlineBatch) > 0 {
		routers := s.buildRouters(req.GatewayId, req.OnlineBatch)
		if err := s.routerRepo.BatchSetUserGateway(ctx, routers); err != nil {
			s.logger.Error("failed to batch set user gateways",
				clog.Int("count", len(routers)),
				clog.Error(err))
		}
	}

	// 2. 批量处理下线列表
	if len(req.OfflineBatch) > 0 {
		usernames := s.buildUsernames(req.OfflineBatch)
		if err := s.routerRepo.BatchDeleteUserGateway(ctx, usernames); err != nil {
			s.logger.Error("failed to batch delete user gateways",
				clog.Int("count", len(usernames)),
				clog.Error(err))
		}
	}

	return &logicv1.SyncStatusResponse{
		SeqId: req.SeqId,
		Error: "", // 这里简单返回空，实际可以聚合错误信息
	}, nil
}

// buildRouters 从上线事件构建 Router 列表
func (s *PresenceService) buildRouters(gatewayID string, onlineBatch []*logicv1.UserOnline) []*model.Router {
	routers := make([]*model.Router, 0, len(onlineBatch))
	for _, online := range onlineBatch {
		routers = append(routers, &model.Router{
			Username:  online.Username,
			GatewayID: gatewayID,
			RemoteIP:  online.RemoteIp,
			Timestamp: online.Timestamp,
		})
	}
	return routers
}

// buildUsernames 从下线事件构建用户名列表
func (s *PresenceService) buildUsernames(offlineBatch []*logicv1.UserOffline) []string {
	usernames := make([]string, 0, len(offlineBatch))
	for _, offline := range offlineBatch {
		usernames = append(usernames, offline.Username)
	}
	return usernames
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
