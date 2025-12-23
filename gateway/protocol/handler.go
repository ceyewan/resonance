package protocol

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"google.golang.org/protobuf/proto"
)

// Handler 处理 WebSocket 消息的接口
type Handler interface {
	// HandlePacket 处理接收到的 WsPacket
	HandlePacket(ctx context.Context, conn Connection, packet *gatewayv1.WsPacket) error
}

// Connection 表示一个 WebSocket 连接的抽象
type Connection interface {
	// Send 发送消息到客户端
	Send(packet *gatewayv1.WsPacket) error
	// Close 关闭连接
	Close() error
	// Username 获取连接对应的用户名
	Username() string
	// RemoteAddr 获取远程地址
	RemoteAddr() string
}

// DefaultHandler 默认的消息处理器
type DefaultHandler struct {
	logger  clog.Logger
	onPulse func(ctx context.Context, conn Connection) error
	onChat  func(ctx context.Context, conn Connection, chat *gatewayv1.ChatRequest) error
	onAck   func(ctx context.Context, conn Connection, ack *gatewayv1.Ack) error
}

// NewDefaultHandler 创建默认处理器
func NewDefaultHandler(
	logger clog.Logger,
	onPulse func(ctx context.Context, conn Connection) error,
	onChat func(ctx context.Context, conn Connection, chat *gatewayv1.ChatRequest) error,
	onAck func(ctx context.Context, conn Connection, ack *gatewayv1.Ack) error,
) *DefaultHandler {
	return &DefaultHandler{
		logger:  logger,
		onPulse: onPulse,
		onChat:  onChat,
		onAck:   onAck,
	}
}

// HandlePacket 实现 Handler 接口
func (h *DefaultHandler) HandlePacket(ctx context.Context, conn Connection, packet *gatewayv1.WsPacket) error {
	switch payload := packet.Payload.(type) {
	case *gatewayv1.WsPacket_Pulse:
		// 心跳消息
		if h.onPulse != nil {
			return h.onPulse(ctx, conn)
		}
		return nil

	case *gatewayv1.WsPacket_Chat:
		// 聊天消息
		if h.onChat != nil {
			return h.onChat(ctx, conn, payload.Chat)
		}
		return nil

	case *gatewayv1.WsPacket_Ack:
		// 确认消息
		if h.onAck != nil {
			return h.onAck(ctx, conn, payload.Ack)
		}
		return nil

	default:
		return fmt.Errorf("unknown packet type: %T", payload)
	}
}

// EncodePacket 将 WsPacket 编码为字节流
func EncodePacket(packet *gatewayv1.WsPacket) ([]byte, error) {
	return proto.Marshal(packet)
}

// DecodePacket 将字节流解码为 WsPacket
func DecodePacket(data []byte) (*gatewayv1.WsPacket, error) {
	packet := &gatewayv1.WsPacket{}
	if err := proto.Unmarshal(data, packet); err != nil {
		return nil, err
	}
	return packet, nil
}

// CreatePulseResponse 创建心跳响应
func CreatePulseResponse(seq string) *gatewayv1.WsPacket {
	return &gatewayv1.WsPacket{
		Seq: seq,
		Payload: &gatewayv1.WsPacket_Pulse{
			Pulse: &gatewayv1.Pulse{},
		},
	}
}

// CreatePushPacket 创建推送消息包
func CreatePushPacket(seq string, msg *gatewayv1.PushMessage) *gatewayv1.WsPacket {
	return &gatewayv1.WsPacket{
		Seq: seq,
		Payload: &gatewayv1.WsPacket_Push{
			Push: msg,
		},
	}
}

// CreateAckPacket 创建确认消息包
func CreateAckPacket(refSeq string) *gatewayv1.WsPacket {
	return &gatewayv1.WsPacket{
		Seq: refSeq,
		Payload: &gatewayv1.WsPacket_Ack{
			Ack: &gatewayv1.Ack{
				RefSeq: refSeq,
			},
		},
	}
}
