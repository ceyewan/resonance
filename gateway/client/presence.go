package client

import (
	"context"
	"io"
	"time"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// presenceStreamWrapper Presence 流包装器
type presenceStreamWrapper struct {
	stream logicv1.PresenceService_SyncStatusClient
}

// SyncUserOnline 同步用户上线状态
func (c *Client) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.presenceStream == nil {
		stream, err := c.presenceSvc().SyncStatus(ctx)
		if err != nil {
			return err
		}
		c.presenceStream = &presenceStreamWrapper{
			stream: stream,
		}

		// 启动接收协程
		go c.receivePresenceResponses()
	}

	c.seqID++
	req := &logicv1.SyncStatusRequest{
		SeqId:     c.seqID,
		GatewayId: c.gatewayID,
		OnlineBatch: []*logicv1.UserOnline{
			{
				Username:  username,
				RemoteIp:  remoteIP,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	if err := c.presenceStream.stream.Send(req); err != nil {
		c.presenceStream = nil // 重置流
		return err
	}

	return nil
}

// SyncUserOffline 同步用户下线状态
func (c *Client) SyncUserOffline(ctx context.Context, username string) error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.presenceStream == nil {
		stream, err := c.presenceSvc().SyncStatus(ctx)
		if err != nil {
			return err
		}
		c.presenceStream = &presenceStreamWrapper{
			stream: stream,
		}

		// 启动接收协程
		go c.receivePresenceResponses()
	}

	c.seqID++
	req := &logicv1.SyncStatusRequest{
		SeqId:     c.seqID,
		GatewayId: c.gatewayID,
		OfflineBatch: []*logicv1.UserOffline{
			{
				Username:  username,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	if err := c.presenceStream.stream.Send(req); err != nil {
		c.presenceStream = nil // 重置流
		return err
	}

	return nil
}

// receivePresenceResponses 接收网关操作的响应
func (c *Client) receivePresenceResponses() {
	if c.presenceStream == nil {
		return
	}

	for {
		resp, err := c.presenceStream.stream.Recv()
		if err != nil {
			if err == io.EOF {
				c.logger.Info("presence stream closed")
			} else {
				c.logger.Error("failed to receive presence response", clog.Error(err))
			}
			c.streamMu.Lock()
			c.presenceStream = nil
			c.streamMu.Unlock()
			return
		}

		// 处理响应
		if resp.Error != "" {
			c.logger.Error("presence sync error",
				clog.Int64("seq_id", resp.SeqId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("presence sync ack",
				clog.Int64("seq_id", resp.SeqId))
		}
	}
}
