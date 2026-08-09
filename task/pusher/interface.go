package pusher

// Client 是 Dispatcher 可使用的最小有界推送队列接口。
type Client interface {
	Enqueue(task *PushTask) error
	QueueSize() int
}

// PusherManager 推送管理器接口
// 用于依赖注入和单元测试
type PusherManager interface {
	// Start 启动服务发现
	Start() error

	// GetClient 获取指定 Gateway 的客户端
	GetClient(gatewayID string) (Client, error)

	// Close 关闭所有连接
	Close()
}
