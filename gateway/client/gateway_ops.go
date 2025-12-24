package client

import (
	"context"
	"io"
	"time"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// gatewayOpsStreamWrapper GatewayOps 流包装器
type gatewayOpsStreamWrapper struct {
	stream logicv1.GatewayOpsService_SyncStateClient
}

// SyncUserOnline 同步用户上线状态
func (c *Client) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.gatewayOpsStream == nil {
		stream, err := c.gatewayOpsSvc().SyncState(ctx)
		if err != nil {
			return err
		}
		c.gatewayOpsStream = &gatewayOpsStreamWrapper{
			stream: stream,
		}

		// 启动接收协程
		go c.receiveGatewayOpsResponses()
	}

	c.seqID++
	req := &logicv1.SyncStateRequest{
		SeqId:     c.seqID,
		GatewayId: c.gatewayID,
		Event: &logicv1.SyncStateRequest_Online{
			Online: &logicv1.UserOnline{
				Username:  username,
				RemoteIp:  remoteIP,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	if err := c.gatewayOpsStream.stream.Send(req); err != nil {
		c.gatewayOpsStream = nil // 重置流
		return err
	}

	return nil
}

// SyncUserOffline 同步用户下线状态
func (c *Client) SyncUserOffline(ctx context.Context, username string) error {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()

	// 如果流未建立，先建立连接
	if c.gatewayOpsStream == nil {
		stream, err := c.gatewayOpsSvc().SyncState(ctx)
		if err != nil {
			return err
		}
		c.gatewayOpsStream = &gatewayOpsStreamWrapper{
			stream: stream,
		}

		// 启动接收协程
		go c.receiveGatewayOpsResponses()
	}

	c.seqID++
	req := &logicv1.SyncStateRequest{
		SeqId:     c.seqID,
		GatewayId: c.gatewayID,
		Event: &logicv1.SyncStateRequest_Offline{
			Offline: &logicv1.UserOffline{
				Username:  username,
				Timestamp: time.Now().Unix(),
			},
		},
	}

	if err := c.gatewayOpsStream.stream.Send(req); err != nil {
		c.gatewayOpsStream = nil // 重置流
		return err
	}

	return nil
}

// receiveGatewayOpsResponses 接收网关操作的响应
func (c *Client) receiveGatewayOpsResponses() {
	if c.gatewayOpsStream == nil {
		return
	}

	for {
		resp, err := c.gatewayOpsStream.stream.Recv()
		if err != nil {
			if err == io.EOF {
				c.logger.Info("gateway ops stream closed")
			} else {
				c.logger.Error("failed to receive gateway ops response", clog.Error(err))
			}
			c.streamMu.Lock()
			c.gatewayOpsStream = nil
			c.streamMu.Unlock()
			return
		}

		// 处理响应
		if resp.Error != "" {
			c.logger.Error("gateway ops error",
				clog.Int64("seq_id", resp.SeqId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("gateway ops ack",
				clog.Int64("seq_id", resp.SeqId))
		}
	}
}
