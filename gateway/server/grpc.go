package server

import (
	"net"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/gateway/push"
	"google.golang.org/grpc"
)

// GRPCServer gRPC 服务包装器
type GRPCServer struct {
	logger      clog.Logger
	pushService *push.Service
	server      *grpc.Server
	addr        string
}

// NewGRPCServer 创建 gRPC 服务
func NewGRPCServer(addr string, logger clog.Logger, pushService *push.Service) *GRPCServer {
	return &GRPCServer{
		addr:        addr,
		logger:      logger,
		pushService: pushService,
	}
}

// Start 启动 gRPC 服务
func (s *GRPCServer) Start() error {
	s.server = grpc.NewServer(
		grpc.ChainUnaryInterceptor(push.TraceUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(push.TraceStreamServerInterceptor()),
	)
	s.pushService.RegisterGRPC(s.server)

	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	s.logger.Info("grpc server started", clog.String("addr", s.addr))
	return s.server.Serve(lis)
}

// Stop 停止 gRPC 服务
func (s *GRPCServer) Stop() {
	if s.server != nil {
		s.server.GracefulStop()
	}
}
