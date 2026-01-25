# Issue: RPC 限流保护

## 元信息
- **ID**: GOV-005
- **标题**: 实现服务级别和用户级别的限流
- **优先级**: P1 - 高
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前系统虽然配置了 `ratelimit` 组件，但未实际使用，存在以下风险：
- 恶意用户可高频调用 API 导致服务崩溃
- 突发流量可能击垮数据库
- 缺乏用户级别的公平性保障

## 受影响范围

- `gateway`: 入口流量控制
- `logic`: 核心业务保护
- 所有 gRPC 和 HTTP 接口

## 根因分析

1. `gateway/middleware/ratelimit.go` 存在但未启用
2. Genesis `ratelimit` 组件未集成到调用链
3. 缺少细粒度的限流策略

## 解决方案

### 1. 限流层级

| 层级 | 对象 | 目标 | 算法 |
|------|------|------|------|
| **服务级** | 单个 Logic 实例 | 保护服务不被击垮 | 令牌桶 |
| **用户级** | 单个用户 | 防止单用户滥用 | 滑动窗口 |
| **IP级** | 单个 IP | 防止恶意刷接口 | 固定窗口 |
| **接口级** | 单个接口 | 根据接口重要性差异化限流 | 令牌桶 |

### 2. 限流策略配置

```go
// gateway/config/ratelimit.go

type RateLimitConfig struct {
    // 全局配置
    GlobalQPS int `yaml:"global_qps"` // 总 QPS 限制

    // 用户级配置
    User UserLimit `yaml:"user"`

    // 接口级配置
    Endpoints map[string]EndpointLimit `yaml:"endpoints"`
}

type UserLimit struct {
    SendMessageQPS int `yaml:"send_message_qps"` // 发送消息: 10/s
    GetHistoryQPS  int `yaml:"get_history_qps"`  // 拉历史: 20/s
    DefaultQPS      int `yaml:"default_qps"`      // 默认: 5/s
}

type EndpointLimit struct {
    QPS   int `yaml:"qps"`
    Burst int `yaml:"burst"`
}

// 默认配置
var DefaultRateLimitConfig = &RateLimitConfig{
    GlobalQPS: 10000,
    User: UserLimit{
        SendMessageQPS: 10,
        GetHistoryQPS:  20,
        DefaultQPS:      5,
    },
    Endpoints: map[string]EndpointLimit{
        "/api/v1/chat/send":     {QPS: 1000, Burst: 100},
        "/api/v1/session/list":  {QPS: 500, Burst: 50},
        "/api/v1/auth/login":    {QPS: 100, Burst: 20},
    },
}
```

### 3. 中间件实现

```go
// gateway/middleware/ratelimit.go

type RateLimitMiddleware struct {
    limiter ratelimit.Limiter
    config *config.RateLimitConfig
}

func (m *RateLimitMiddleware) Intercept() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 1. 提取用户标识
        username := m.getUsername(c)

        // 2. 构造限流 key
        key := fmt.Sprintf("user:%s:api:%s", username, c.Request.URL.Path)

        // 3. 检查限流
        allowed, err := m.limiter.Allow(key)
        if err != nil {
            c.AbortWithStatus(http.StatusInternalServerError)
            return
        }

        if !allowed {
            c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
                "error": "rate limit exceeded",
            })
            return
        }

        c.Next()
    }
}
```

### 4. gRPC 拦截器

```go
// gateway/client/client.go

func (c *Client) newRateLimitInterceptor() grpc.UnaryClientInterceptor {
    return func(ctx context.Context, method string, req, reply interface{},
        cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {

        // 构造限流 key
        key := fmt.Sprintf("grpc:%s", method)

        // 检查限流
        if !c.limiter.Allow(key) {
            return status.Errorf(codes.ResourceExhausted, "rate limit exceeded")
        }

        return invoker(ctx, method, req, reply, cc, opts...)
    }
}
```

### 5. 限流算法选择

| 算法 | 适用场景 | 优点 | 缺点 |
|------|----------|------|------|
| **令牌桶** | 平滑限流 | 允许突发 | 实现稍复杂 |
| **漏桶** | 恒定速率 | 流量平滑 | 不允许突发 |
| **固定窗口** | 简单限流 | 实现简单 | 边界突刺 |
| **滑动窗口** | 精确限流 | 精确 | 实现复杂 |

**推荐**: 用户级用滑动窗口，服务级用令牌桶

### 6. Redis 分布式限流

```go
// 使用 Redis 实现分布式限流

type RedisLimiter struct {
    client redis.Cmdable
}

func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
    // Lua 脚本保证原子性
    script := `
        local key = KEYS[1]
        local limit = tonumber(ARGV[1])
        local window = tonumber(ARGV[2])
        local now = tonumber(ARGV[3])

        -- 清理过期的计数器
        redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)

        -- 获取当前计数
        local count = redis.call('ZCARD', key)

        if count < limit then
            redis.call('ZADD', key, now, now)
            redis.call('EXPIRE', key, window)
            return 1
        else
            return 0
        end
    `

    result, err := l.client.Eval(ctx, script, []string{key},
        limit, window.Seconds(), time.Now().UnixMilli()).Int()
    if err != nil {
        return false, err
    }

    return result == 1, nil
}
```

### 7. 监控指标

```go
var (
    rateLimitTotal = metrics.NewCounter("rate_limit_total", "Total rate limit hits")
    rateLimitDenied = metrics.NewCounter("rate_limit_denied_total", "Denied requests")
)
```

## 验收标准

- [ ] 限流中间件已启用
- [ ] 用户级限流生效
- [ ] 服务级限流生效
- [ ] 限流后返回 429 状态码
- [ ] Prometheus 指标正常上报
- [ ] 压测验证限流准确性

## 参考链接

- Genesis ratelimit 组件文档
- Redis 限流最佳实践
- gRPC 拦截器文档
