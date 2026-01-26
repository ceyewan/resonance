package server

import (
	"context"
	"net"
	"time"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/logic/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCServer gRPC 服务包装器
type GRPCServer struct {
	logger      clog.Logger
	server      *grpc.Server
	addr        string
	authSvc     *service.AuthService
	sessionSvc  *service.SessionService
	chatSvc     *service.ChatService
	presenceSvc *service.PresenceService
}

// NewGRPCServer 创建 gRPC 服务
func NewGRPCServer(
	addr string,
	logger clog.Logger,
	authSvc *service.AuthService,
	sessionSvc *service.SessionService,
	chatSvc *service.ChatService,
	presenceSvc *service.PresenceService,
) *GRPCServer {
	return &GRPCServer{
		addr:        addr,
		logger:      logger,
		authSvc:     authSvc,
		sessionSvc:  sessionSvc,
		chatSvc:     chatSvc,
		presenceSvc: presenceSvc,
	}
}

// Start 启动 gRPC 服务
func (s *GRPCServer) Start() error {
	// 创建 gRPC Server，添加通用拦截器
	s.server = grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			s.recoveryUnaryInterceptor,
			s.loggerUnaryInterceptor,
		),
		grpc.ChainStreamInterceptor(
			s.recoveryStreamInterceptor,
			s.loggerStreamInterceptor,
		),
	)

	// 注册服务
	logicv1.RegisterAuthServiceServer(s.server, s.authSvc)
	logicv1.RegisterSessionServiceServer(s.server, s.sessionSvc)
	logicv1.RegisterChatServiceServer(s.server, s.chatSvc)
	logicv1.RegisterPresenceServiceServer(s.server, s.presenceSvc)

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

// recoveryUnaryInterceptor 恢复拦截器 (Unary)
func (s *GRPCServer) recoveryUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("grpc panic recovered", clog.Any("panic", r))
			err = status.Errorf(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, req)
}

// recoveryStreamInterceptor 恢复拦截器 (Stream)
func (s *GRPCServer) recoveryStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("grpc panic recovered", clog.Any("panic", r))
			err = status.Errorf(codes.Internal, "internal server error")
		}
	}()
	return handler(srv, ss)
}

// loggerUnaryInterceptor 日志拦截器 (Unary)
// 策略：错误日志记录为 Error，慢请求（>100ms）记录为 Warn，正常请求记录为 Debug
func (s *GRPCServer) loggerUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	startTime := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(startTime)

	// 提取 trace_id
	var traceID string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("trace-id"); len(vals) > 0 {
			traceID = vals[0]
		}
	}

	fields := []clog.Field{
		clog.String("method", info.FullMethod),
		clog.Duration("duration", duration),
		clog.String("trace_id", traceID),
	}

	if err != nil {
		// 错误请求：记录为 Error
		fields = append(fields, clog.Error(err))
		s.logger.Error("grpc call failed", fields...)
	} else if duration > 100*time.Millisecond {
		// 慢请求（>100ms）：记录为 Warn
		s.logger.Warn("grpc call slow", fields...)
	} else {
		// 正常请求：记录为 Debug
		s.logger.Debug("grpc call success", fields...)
	}

	return resp, err
}

// loggerStreamInterceptor 日志拦截器 (Stream)
// 策略：错误日志记录为 Error，慢请求（>100ms）记录为 Warn，正常请求记录为 Debug
func (s *GRPCServer) loggerStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	startTime := time.Now()
	err := handler(srv, ss)
	duration := time.Since(startTime)

	// Stream 无法轻易获取 trace_id，这里简化处理
	fields := []clog.Field{
		clog.String("method", info.FullMethod),
		clog.Duration("duration", duration),
	}

	if err != nil {
		// 错误请求：记录为 Error
		fields = append(fields, clog.Error(err))
		s.logger.Error("grpc stream finished with error", fields...)
	} else if duration > 100*time.Millisecond {
		// 慢请求（>100ms）：记录为 Warn
		s.logger.Warn("grpc stream slow", fields...)
	} else {
		// 正常请求：记录为 Debug
		s.logger.Debug("grpc stream finished", fields...)
	}

	return err
}
