package client

import (
	"context"
	"fmt"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// SendMessage 发送消息到 Logic（Unary 调用）
func (c *Client) SendMessage(ctx context.Context, msg *gatewayv1.ChatRequest) (*logicv1.SendMessageResponse, error) {
	if c.chatClient == nil {
		return nil, fmt.Errorf("chat client not initialized")
	}

	req := &logicv1.SendMessageRequest{
		SessionId:    msg.SessionId,
		FromUsername: msg.FromUsername,
		ToUsername:   msg.ToUsername,
		Content:      msg.Content,
		Type:         msg.Type,
		Timestamp:    msg.Timestamp,
	}

	return c.chatClient.SendMessage(ctx, req)
}
