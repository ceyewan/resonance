package client

import (
	"context"
	"io"
	"sync"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// chatStreamWrapper 聊天流包装器
type chatStreamWrapper struct {
	stream      logicv1.ChatService_SendMessageClient
	client      *Client
	receiveOnce sync.Once
}

// SendMessage 发送消息到 Logic（通过双向流）
func (c *Client) SendMessage(ctx context.Context, msg *gatewayv1.ChatRequest) (*logicv1.SendMessageResponse, error) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.chatStream == nil {
		stream, err := c.chatSvc().SendMessage(ctx)
		if err != nil {
			return nil, err
		}
		c.chatStream = &chatStreamWrapper{
			stream: stream,
			client: c,
		}

		// 启动接收协程
		go c.receiveChatResponses()
	}

	// 发送消息
	req := &logicv1.SendMessageRequest{
		SessionId:    msg.SessionId,
		FromUsername: msg.FromUsername,
		ToUsername:   msg.ToUsername,
		Content:      msg.Content,
		Type:         msg.Type,
		Timestamp:    msg.Timestamp,
	}

	if err := c.chatStream.stream.Send(req); err != nil {
		c.chatStream = nil // 重置流
		return nil, err
	}

	// 注意：这里简化处理，实际应该等待对应的响应
	return &logicv1.SendMessageResponse{}, nil
}

// receiveChatResponses 接收聊天消息的响应
func (c *Client) receiveChatResponses() {
	if c.chatStream == nil {
		return
	}

	for {
		resp, err := c.chatStream.stream.Recv()
		if err != nil {
			if err == io.EOF {
				c.logger.Info("chat stream closed")
			} else {
				c.logger.Error("failed to receive chat response", clog.Error(err))
			}
			c.streamMu.Lock()
			c.chatStream = nil
			c.streamMu.Unlock()
			return
		}

		// 处理响应（这里可以添加回调或通知机制）
		if resp.Error != "" {
			c.logger.Error("chat message error",
				clog.Int64("msg_id", resp.MsgId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("chat message sent",
				clog.Int64("msg_id", resp.MsgId),
				clog.Int64("seq_id", resp.SeqId))
		}
	}
}
