package client

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// StatusBatcher 聚合用户在线状态变更，批量同步到 Logic
// 采用双重触发机制：数量触发 + 时间触发
type StatusBatcher struct {
	client   logicv1.PresenceServiceClient
	gatewayID string
	logger   clog.Logger

	// 批量配置
	batchSize int    // 数量触发阈值
	flushInterval time.Duration // 时间触发间隔

	// 缓冲区
	onlineBuf  []*logicv1.UserOnline
	offlineBuf []*logicv1.UserOffline

	// 同步控制
	mu       sync.Mutex
	seq      atomic.Int64
	stopCh   chan struct{}
	wg       sync.WaitGroup
	running  atomic.Bool
}

// BatcherOption 配置 StatusBatcher 的选项
type BatcherOption func(*StatusBatcher)

// WithBatchSize 设置批量大小阈值
func WithBatchSize(size int) BatcherOption {
	return func(b *StatusBatcher) {
		b.batchSize = size
	}
}

// WithFlushInterval 设置刷新间隔
func WithFlushInterval(interval time.Duration) BatcherOption {
	return func(b *StatusBatcher) {
		b.flushInterval = interval
	}
}

// NewStatusBatcher 创建状态批量同步器
func NewStatusBatcher(
	client logicv1.PresenceServiceClient,
	gatewayID string,
	logger clog.Logger,
	opts ...BatcherOption,
) *StatusBatcher {
	b := &StatusBatcher{
		client:        client,
		gatewayID:     gatewayID,
		logger:        logger,
		batchSize:     50,  // 默认 50 条触发
		flushInterval: 100 * time.Millisecond, // 默认 100ms 触发
		onlineBuf:     make([]*logicv1.UserOnline, 0, 50),
		offlineBuf:    make([]*logicv1.UserOffline, 0, 50),
		stopCh:        make(chan struct{}),
	}

	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Start 启动批量同步器
func (b *StatusBatcher) Start() {
	if !b.running.CompareAndSwap(false, true) {
		b.logger.Warn("status batcher already running")
		return
	}

	b.logger.Info("status batcher starting",
		clog.Int("batch_size", b.batchSize),
		clog.Duration("flush_interval", b.flushInterval))

	b.wg.Add(1)
	go b.flushLoop()
}

// Stop 停止批量同步器
func (b *StatusBatcher) Stop() {
	if !b.running.CompareAndSwap(true, false) {
		return
	}

	close(b.stopCh)
	b.wg.Wait()

	// 最后一次刷新
	b.flush()

	b.logger.Info("status batcher stopped")
}

// SyncUserOnline 同步用户上线（异步，放入缓冲区）
func (b *StatusBatcher) SyncUserOnline(username, remoteIP string) {
	event := &logicv1.UserOnline{
		Username:  username,
		RemoteIp:  remoteIP,
		Timestamp: time.Now().Unix(),
	}

	b.mu.Lock()
	b.onlineBuf = append(b.onlineBuf, event)
	shouldFlush := len(b.onlineBuf)+len(b.offlineBuf) >= b.batchSize
	b.mu.Unlock()

	if shouldFlush {
		b.flush()
	}
}

// SyncUserOffline 同步用户下线（异步，放入缓冲区）
func (b *StatusBatcher) SyncUserOffline(username string) {
	event := &logicv1.UserOffline{
		Username:  username,
		Timestamp: time.Now().Unix(),
	}

	b.mu.Lock()
	b.offlineBuf = append(b.offlineBuf, event)
	shouldFlush := len(b.onlineBuf)+len(b.offlineBuf) >= b.batchSize
	b.mu.Unlock()

	if shouldFlush {
		b.flush()
	}
}

// flushLoop 定时刷新循环
func (b *StatusBatcher) flushLoop() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.flush()
		case <-b.stopCh:
			return
		}
	}
}

// flush 将缓冲区数据批量发送到 Logic
func (b *StatusBatcher) flush() {
	b.mu.Lock()

	if len(b.onlineBuf) == 0 && len(b.offlineBuf) == 0 {
		b.mu.Unlock()
		return
	}

	// 复制数据并清空缓冲区
	onlineBatch := make([]*logicv1.UserOnline, len(b.onlineBuf))
	copy(onlineBatch, b.onlineBuf)
	b.onlineBuf = b.onlineBuf[:0]

	offlineBatch := make([]*logicv1.UserOffline, len(b.offlineBuf))
	copy(offlineBatch, b.offlineBuf)
	b.offlineBuf = b.offlineBuf[:0]

	seqID := b.seq.Add(1)
	b.mu.Unlock()

	// 构造请求
	req := &logicv1.SyncStatusRequest{
		SeqId:        seqID,
		GatewayId:    b.gatewayID,
		OnlineBatch:  onlineBatch,
		OfflineBatch: offlineBatch,
	}

	// 发送（带超时）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := b.client.SyncStatus(ctx, req)
	if err != nil {
		b.logger.Error("failed to sync status",
			clog.Int("online_count", len(onlineBatch)),
			clog.Int("offline_count", len(offlineBatch)),
			clog.Error(err))
		return
	}

	if resp.Error != "" {
		b.logger.Error("sync status returned error",
			clog.String("error", resp.Error),
			clog.Int64("seq_id", resp.SeqId))
		return
	}

	b.logger.Debug("status synced successfully",
		clog.Int("online_count", len(onlineBatch)),
		clog.Int("offline_count", len(offlineBatch)),
		clog.Int64("seq_id", resp.SeqId))
}
