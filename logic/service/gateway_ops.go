package service

import (
	"context"
	"io"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
)

// GatewayOpsService 网关操作服务
type GatewayOpsService struct {
	logicv1.UnimplementedGatewayOpsServiceServer
	routerRepo repo.RouterRepo
	logger     clog.Logger
}

// NewGatewayOpsService 创建网关操作服务
func NewGatewayOpsService(
	routerRepo repo.RouterRepo,
	logger clog.Logger,
) *GatewayOpsService {
	return &GatewayOpsService{
		routerRepo: routerRepo,
		logger:     logger,
	}
}

// SyncState 实现 GatewayOpsService.SyncState（双向流）
func (s *GatewayOpsService) SyncState(srv logicv1.GatewayOpsService_SyncStateServer) error {
	s.logger.Info("gateway ops stream established")

	for {
		req, err := srv.Recv()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("gateway ops stream closed by client")
				return nil
			}
			s.logger.Error("failed to receive state sync", clog.Error(err))
			return err
		}

		// 处理状态同步
		resp := s.handleStateSync(srv.Context(), req)

		// 发送响应
		if err := srv.Send(resp); err != nil {
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

	// 保存用户路由信息到 RouterRepo
	router := &model.Router{
		Username:  event.Username,
		GatewayID: gatewayID,
		RemoteIP:  event.RemoteIp,
		Timestamp: event.Timestamp,
	}

	if err := s.routerRepo.SetUserGateway(ctx, router); err != nil {
		s.logger.Error("failed to set user gateway", clog.Error(err))
		return &logicv1.SyncStateResponse{
			SeqId: seqID,
			Error: "failed to set user gateway",
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

	// 删除用户路由信息
	if err := s.routerRepo.DeleteUserGateway(ctx, event.Username); err != nil {
		s.logger.Error("failed to delete user gateway", clog.Error(err))
		return &logicv1.SyncStateResponse{
			SeqId: seqID,
			Error: "failed to delete user gateway",
		}
	}

	return &logicv1.SyncStateResponse{
		SeqId: seqID,
		Error: "",
	}
}

// IsUserOnline 检查用户是否在线
func (s *GatewayOpsService) IsUserOnline(ctx context.Context, username string) (bool, string, error) {
	router, err := s.routerRepo.GetUserGateway(ctx, username)
	if err != nil {
		return false, "", err
	}
	if router == nil {
		return false, "", nil
	}
	return true, router.GatewayID, nil
}
