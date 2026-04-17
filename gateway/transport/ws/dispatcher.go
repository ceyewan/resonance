package ws

import (
	"context"

	"github.com/ceyewan/genesis/clog"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/logicclient"
)

// Dispatcher 消息分发器
type Dispatcher struct {
	logger      clog.Logger
	logicClient *logicclient.Client
}

// NewDispatcher 创建消息分发器
func NewDispatcher(logger clog.Logger, logicClient *logicclient.Client) *Dispatcher {
	return &Dispatcher{
		logger:      logger,
		logicClient: logicClient,
	}
}

// HandlePulse 处理心跳消息
func (d *Dispatcher) HandlePulse(ctx context.Context, conn Connection) error {
	packet := CreatePulseResponse("")
	return conn.Send(packet)
}

// HandleChat 处理聊天消息
func (d *Dispatcher) HandleChat(ctx context.Context, conn Connection, seq string, chat *gatewayv1.ChatRequest) error {
	// 调用 Logic 服务处理消息
	resp, err := d.logicClient.SendEvent(ctx, conn.Username(), chat)
	var eventID, seqID int64
	if err != nil {
	} else if resp != nil {
		eventID = resp.GetEventId()
		seqID = resp.GetSeqId()
	}

	// 发送确认给客户端，包含服务端生成的 ID
	ackPacket := CreateAckPacket(seq, eventID, seqID, chat.SessionId)
	if err := conn.Send(ackPacket); err != nil {
		d.logger.Error("failed to send ack", clog.Error(err))
		return err
	}

	if err != nil {
		return err
	}
	return nil
}

// HandleAck 处理确认消息
func (d *Dispatcher) HandleAck(ctx context.Context, conn Connection, ack *gatewayv1.Ack) error {
	// 目前仅作为连接活跃的信号，暂无特殊逻辑
	return nil
}
