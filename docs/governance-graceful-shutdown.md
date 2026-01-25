# Issue: Gateway 优雅关闭

## 元信息
- **ID**: GOV-003
- **标题**: 实现服务优雅关闭机制
- **优先级**: P1 - 高
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前服务收到退出信号（SIGTERM/SIGINT）时直接退出，导致：
- 正在处理的请求被中断
- WebSocket 连接强制关闭，用户体验差
- 未完成的消息丢失
- 资源未正确释放（连接池、文件句柄）

## 受影响范围

- `gateway/gateway.go`: 主进程生命周期
- `gateway/connection/manager.go`: 连接管理
- `logic/logic.go`: 逻辑服务生命周期
- `task/task.go`: 任务服务生命周期

## 根因分析

1. Close() 方法存在但等待时间可能不足
2. MQ 消费者未等待当前消息处理完成
3. WebSocket 连接未发送关闭帧
4. 数据库事务未回滚

## 解决方案

### 1. 优雅关闭流程

```
收到退出信号
    │
    ▼
1. 停止接受新请求/连接
    │
    ▼
2. 等待现有请求处理完成（超时 30s）
    │
    ▼
3. 关闭 WebSocket 连接（发送 CloseFrame）
    │
    ▼
4. 等待 MQ 消费者处理完当前消息
    │
    ▼
5. 刷新缓存（如有必要）
    │
    ▼
6. 关闭数据库/Redis/ETCD 连接
    │
    ▼
7. 进程退出
```

### 2. 实现要点

#### Gateway 服务

```go
// gateway/gateway.go
func (g *Gateway) Close() error {
    g.logger.Info("shutting down gateway...")

    // 1. 停止接受新连接
    g.httpServer.Shutdown(ctx)
    g.grpcServer.GracefulStop()

    // 2. 等待现有请求完成
    waitForRequestsToComplete(30 * time.Second)

    // 3. 关闭所有 WebSocket 连接
    g.resources.connMgr.CloseAll(websocket.CloseNormalClosure, "server shutdown")

    // 4. 关闭 StatusBatcher
    g.resources.logicClient.stopStatusBatcher()

    // 5. 关闭资源连接
    g.resources.logicClient.Close()
    g.resources.redisConn.Close()
    g.resources.etcdConn.Close()

    return nil
}
```

#### Logic 服务

```go
// logic/logic.go
func (l *Logic) Close() error {
    l.logger.Info("shutting down logic...")

    // 1. 停止接受新请求
    l.grpcServer.GracefulStop()

    // 2. 等待现有 RPC 完成（内置）

    // 3. 关闭资源
    l.dbConn.Close()
    l.redisConn.Close()
    l.mqClient.Close()

    return nil
}
```

#### Task 服务

```go
// task/task.go
func (t *Task) Close() error {
    t.logger.Info("shutting down task...")

    // 1. 停止消费者（但等待当前消息处理完成）
    t.consumer.Stop()

    // 2. 等待推送任务完成
    t.pusherManager.Stop()

    // 3. 关闭资源
    t.dbConn.Close()
    t.mqClient.Close()

    return nil
}
```

### 3. 信号处理

```go
// main.go
func main() {
    app := NewApp()

    // 监听信号
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

    <-sigCh
    log.Info("received shutdown signal")

    // 优雅关闭（带超时）
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := app.Shutdown(ctx); err != nil {
        log.Error("shutdown error", err)
        os.Exit(1)
    }

    log.Info("shutdown complete")
}
```

### 4. 连接管理器改造

```go
// gateway/connection/manager.go
func (m *Manager) CloseAll(code int, reason string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    for _, conn := range m.conns {
        // 发送关闭帧
        conn.WriteMessage(websocket.CloseMessage,
            websocket.FormatCloseMessage(code, reason))

        // 关闭连接
        conn.Close()
    }

    m.logger.Info("all connections closed",
        clog.Int("count", len(m.conns)))
}
```

### 5. MQ 消费者改造

```go
// task/consumer/consumer.go
type Consumer struct {
    stopCh chan struct{}
    wg     sync.WaitGroup
}

func (c *Consumer) Start() {
    c.wg.Add(1)
    go func() {
        defer c.wg.Done()
        for {
            select {
            case <-c.stopCh:
                return
            default:
                msg, err := c.mqClient.NextMsg(context.Background())
                if err != nil {
                    continue
                }
                c.handleMessage(msg)
            }
        }
    }()
}

func (c *Consumer) Stop() {
    close(c.stopCh)
    c.wg.Wait() // 等待所有消息处理完成
}
```

## 验收标准

- [ ] SIGTERM/SIGINT 信号正确捕获
- [ ] 正在处理的请求完成后才退出
- [ ] WebSocket 连接收到正常关闭通知
- [ ] MQ 消费者处理完当前消息
- [ ] 所有资源正确释放
- [ ] 优雅关闭超时 30 秒后强制退出

## 参考链接

- Go 优雅关闭最佳实践
- gRPC GracefulStop 文档
- WebSocket CloseFrame 规范
