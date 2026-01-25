package pusher

// PusherManager 推送管理器接口
// 用于依赖注入和单元测试
type PusherManager interface {
	// Start 启动服务发现
	Start() error

	// GetClient 获取指定 Gateway 的客户端
	GetClient(gatewayID string) (*GatewayClient, error)

	// Close 关闭所有连接
	Close()
}

