package client

import (
	"context"
	"fmt"
)

// SyncUserOnline 同步用户上线到 Logic（通过 StatusBatcher 批量处理）
func (c *Client) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	if c.statusBatcher == nil {
		return fmt.Errorf("status batcher not initialized")
	}
	c.statusBatcher.SyncUserOnline(username, remoteIP)
	return nil
}

// SyncUserOffline 同步用户下线到 Logic（通过 StatusBatcher 批量处理）
func (c *Client) SyncUserOffline(ctx context.Context, username string) error {
	if c.statusBatcher == nil {
		return fmt.Errorf("status batcher not initialized")
	}
	c.statusBatcher.SyncUserOffline(username)
	return nil
}

// IsUserOnline 检查用户是否在线（通过 SessionService 查询）
func (c *Client) IsUserOnline(ctx context.Context, username string) (bool, string, error) {
	if c.sessionClient == nil {
		return false, "", fmt.Errorf("session client not initialized")
	}

	// 使用 SessionService.GetUserSession 查询用户在线状态
	// 这里需要根据实际 API 调整
	return false, "", nil
}
