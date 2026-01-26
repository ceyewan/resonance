package ws

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/protocol"
)

// Dispatcher 消息分发器
type Dispatcher struct {
	logger      clog.Logger
	logicClient *client.Client
}

// NewDispatcher 创建消息分发器
func NewDispatcher(logger clog.Logger, logicClient *client.Client) *Dispatcher {
	return &Dispatcher{
		logger:      logger,
		logicClient: logicClient,
	}
}

// HandlePulse 处理心跳消息
func (d *Dispatcher) HandlePulse(ctx context.Context, conn protocol.Connection) error {
	packet := protocol.CreatePulseResponse("")
	return conn.Send(packet)
}

// HandleChat 处理聊天消息
func (d *Dispatcher) HandleChat(ctx context.Context, conn protocol.Connection, seq string, chat *gatewayv1.ChatRequest) error {
	// 填充发送者
	if chat.FromUsername == "" {
		chat.FromUsername = conn.Username()
	}
	// 填充时间戳
	if chat.Timestamp == 0 {
		chat.Timestamp = time.Now().Unix()
	}

	// 调用 Logic 服务处理消息
	resp, err := d.logicClient.SendMessage(ctx, chat)
	var ackErr string
	var msgID, seqID int64
	if err != nil {
		ackErr = err.Error()
	} else if resp != nil {
		msgID = resp.GetMsgId()
		seqID = resp.GetSeqId()
		ackErr = resp.GetError()
	}

	// 发送确认给客户端，包含服务端生成的 ID
	ackPacket := protocol.CreateAckPacket(seq, msgID, seqID, chat.SessionId, ackErr)
	if err := conn.Send(ackPacket); err != nil {
		d.logger.Error("failed to send ack", clog.Error(err))
		return err
	}

	if err != nil {
		return err
	}
	if ackErr != "" {
		return fmt.Errorf(ackErr)
	}
	return nil
}

// HandleAck 处理确认消息
func (d *Dispatcher) HandleAck(ctx context.Context, conn protocol.Connection, ack *gatewayv1.Ack) error {
	// 目前仅作为连接活跃的信号，暂无特殊逻辑
	return nil
}
