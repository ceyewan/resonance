# Issue: 服务健康检查机制

## 元信息

- **ID**: GOV-004
- **标题**: 实现服务健康检查端点
- **优先级**: P1 - 高
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前系统缺少健康检查机制，导致：

- K8s/容器编排无法判断服务是否健康
- 不健康的实例无法自动摘除
- 部署时无法判断服务是否真正可用
- 缺少依赖服务的健康状态检查

## 受影响范围

- `gateway`, `logic`, `task`: 所有服务
- 部署平台：Kubernetes / Docker Compose

## 根因分析

1. 无 `/healthz` 或 `/metrics` 端点
2. 服务启动不代表就绪（数据库连接可能未就绪）
3. 缺少对依赖服务（MySQL/Redis/ETCD）的健康检查

## 解决方案

### 1. 健康检查级别

| 级别      | 路径               | 检查内容       | 用途                |
| --------- | ------------------ | -------------- | ------------------- |
| Liveness  | `/healthz/live`    | 进程是否存活   | K8s liveness probe  |
| Readiness | `/healthz/ready`   | 是否能处理请求 | K8s readiness probe |
| Startup   | `/healthz/startup` | 启动是否完成   | K8s startup probe   |

### 2. 检查项设计

#### Liveness（存活检查）

```go
// 只检查进程是否存活
func (s *Server) Livez(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}
```

#### Readiness（就绪检查）

```go
// 检查依赖服务是否可用
func (s *Server) Readyz(w http.ResponseWriter, r *http.Request) {
    // 1. 检查数据库
    if err := s.db.Ping(); err != nil {
        http.Error(w, "db not ready", http.StatusServiceUnavailable)
        return
    }

    // 2. 检查 Redis
    if err := s.redis.Ping(); err != nil {
        http.Error(w, "redis not ready", http.StatusServiceUnavailable)
        return
    }

    // 3. 检查 ETCD（如需要）
    if err := s.etcd.Ping(); err != nil {
        http.Error(w, "etcd not ready", http.StatusServiceUnavailable)
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("Ready"))
}
```

#### Startup（启动检查）

```go
// 检查服务是否完成初始化
func (s *Server) Startupz(w http.ResponseWriter, r *http.Request) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    if !s.initialized {
        http.Error(w, "starting", http.StatusServiceUnavailable)
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("Started"))
}
```

### 3. Gateway 特有检查

```go
// Gateway 额外检查推送通道是否可用
func (g *Gateway) Readyz(w http.ResponseWriter, r *http.Request) {
    // 基础检查...
    if err := basicChecks(); err != nil {
        http.Error(w, err.Error(), http.StatusServiceUnavailable)
        return
    }

    // 检查与 Logic 的连接
    if err := g.logicClient.Ping(); err != nil {
        http.Error(w, "logic not ready", http.StatusServiceUnavailable)
        return
    }

    w.WriteHeader(http.StatusOK)
}
```

### 4. K8s Probe 配置

```yaml
# k8s/deployment.yaml
spec:
    containers:
        - name: gateway
          image: resonance/gateway:latest
          ports:
              - containerPort: 8080
              - containerPort: 9090 # health check port
          livenessProbe:
              httpGet:
                  path: /healthz/live
                  port: 9090
              initialDelaySeconds: 10
              periodSeconds: 10
          readinessProbe:
              httpGet:
                  path: /healthz/ready
                  port: 9090
              initialDelaySeconds: 5
              periodSeconds: 5
          startupProbe:
              httpGet:
                  path: /healthz/startup
                  port: 9090
              initialDelaySeconds: 0
              periodSeconds: 2
              failureThreshold: 30 # 最多等待 60 秒
```

### 5. Docker Compose 健康检查

```yaml
# docker-compose.yml
services:
    gateway:
        image: resonance/gateway:latest
        healthcheck:
            test: ["CMD", "curl", "-f", "http://localhost:9090/healthz/ready"]
            interval: 5s
            timeout: 3s
            retries: 3
            start_period: 30s
        depends_on:
            logic:
                condition: service_healthy
```

### 6. 详细健康信息（可选）

返回 JSON 格式的详细健康状态：

```go
type HealthStatus struct {
    Status    string            `json:"status"`     // healthy, degraded, unhealthy
    Timestamp int64             `json:"timestamp"`
    Checks    map[string]Check  `json:"checks"`
}

type Check struct {
    Status  string `json:"status"`
    Message string `json:"message,omitempty"`
}

func (s *Server) Healthz(w http.ResponseWriter, r *http.Request) {
    status := &HealthStatus{
        Status:    "healthy",
        Timestamp: time.Now().Unix(),
        Checks:    make(map[string]Check),
    }

    // 检查数据库
    if err := s.db.Ping(); err != nil {
        status.Checks["database"] = Check{Status: "unhealthy", Message: err.Error()}
        status.Status = "unhealthy"
    } else {
        status.Checks["database"] = Check{Status: "healthy"}
    }

    // 检查 Redis
    if err := s.redis.Ping(); err != nil {
        status.Checks["redis"] = Check{Status: "unhealthy", Message: err.Error()}
        status.Status = "degraded"
    } else {
        status.Checks["redis"] = Check{Status: "healthy"}
    }

    // 返回相应状态码
    switch status.Status {
    case "healthy":
        w.WriteHeader(http.StatusOK)
    case "degraded":
        w.WriteHeader(http.StatusServiceUnavailable) // 或 200
    default:
        w.WriteHeader(http.StatusServiceUnavailable)
    }

    json.NewEncoder(w).Encode(status)
}
```

## 验收标准

- [ ] 所有服务提供 `/healthz/live` 端点
- [ ] 所有服务提供 `/healthz/ready` 端点
- [ ] 依赖服务不可用时 readiness 失败
- [ ] K8s probe 配置完成
- [ ] 不健康实例可自动重启

## 参考链接

- Kubernetes Probe 文档
- Docker Healthcheck 文档
