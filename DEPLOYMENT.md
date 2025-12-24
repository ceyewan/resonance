# 📦 Resonance IM 部署指南

## 🏗️ 架构概览

Resonance IM 采用微服务架构，分为三个核心服务：

```
┌─────────────────────────────────────────────────────────┐
│                    Web 前端客户端                          │
│              (HTTP + WebSocket 连接)                      │
└────────────────────┬────────────────────────────────────┘
                     │
        ┌────────────┴────────────┐
        │                         │
        ▼                         ▼
   HTTP/JSON              WebSocket
   (ConnectRPC)           (自定义协议)
        │                         │
        └────────────┬────────────┘
                     │
                     ▼
         ┌─────────────────────┐
         │   Gateway Service   │  ◄─── 对外暴露的服务
         │  (公网可访问)        │
         └────────┬────────────┘
                  │
        gRPC (内部网络)
                  │
        ┌─────────┴─────────┐
        ▼                   ▼
    ┌────────────┐    ┌────────────┐
    │   Logic    │◄──►│    Task    │
    │  Service   │MQ  │  Service   │
    └────────────┘    └────────────┘
        (内网)            (内网)
```

## 🔑 核心概念

### 外部服务地址（External Address）
**对外暴露给客户端的服务地址**

- **Gateway HTTP API**: `http://gateway.example.com:8080` 或 `http://api.example.com`
- **Gateway WebSocket**: `ws://gateway.example.com:8081` 或 `wss://api.example.com`
- **说明**：
  - 由前端配置通过 `VITE_API_BASE_URL` 和 `VITE_WS_URL` 指定
  - 可以是公网域名、IP、或负载均衡地址
  - 需要通过 Nginx/HAProxy 等反向代理暴露

### 内部服务地址（Internal Address）
**仅在服务间通信中使用的地址**

- **Gateway 内部**: `gateway:9091` (gRPC)
- **Logic 内部**: `logic:9090` (gRPC)
- **Task 内部**: 通过 Registry 服务发现
- **说明**：
  - 各服务在自己的 `config.yaml` 中配置
  - 通过 Docker Compose 或 Kubernetes 的服务发现
  - 外部无需关心，由内部通过 Registry 管理

## 📋 配置详解

### 1. Gateway 配置

**`configs/gateway.dev.yaml`** (开发环境 - 本地运行)
```yaml
service:
  name: gateway
  http_addr: :8080        # 内部监听地址
  ws_addr: :8081          # 内部监听地址

logic_addr: localhost:9090  # 连接到本地 Logic
```

**`configs/gateway.prod.yaml`** (生产环境 - Docker 网络)
```yaml
service:
  name: gateway
  http_addr: :8080        # 容器内监听
  ws_addr: :8081          # 容器内监听

logic_addr: logic:9090    # 通过 Docker DNS 连接到 Logic
```

**前端如何访问？**
- 开发：`http://localhost:8080` (Makefile 自动配置)
- 生产：通过 Nginx/HAProxy 反向代理，对外暴露为 `http://api.example.com`

### 2. Logic 配置

**`configs/logic.dev.yaml`** (开发环境)
```yaml
service:
  name: logic
  server_addr: :9090    # 只监听本地端口，不对外暴露

mysql:
  host: localhost
redis:
  addr: localhost:6379
```

**`configs/logic.prod.yaml`** (生产环境)
```yaml
service:
  name: logic
  server_addr: :9090    # 只在 Docker 内网监听

mysql:
  host: mysql-service   # Docker 服务名
redis:
  addr: redis-service:6379
```

### 3. Task 配置

**`configs/task.prod.yaml`** (生产环境)
```yaml
service:
  name: task

mysql:
  host: mysql-service
redis:
  addr: redis-service:6379
nats:
  url: nats://nats-service:4222
etcd:
  endpoints:
    - etcd-service:2379

registry:
  namespace: /resonance/services
  default_ttl: 30s
```

### 4. 前端环境变量配置

**`web/.env.development`** (开发环境)
```
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_URL=ws://localhost:8081/ws
```

**`web/.env.production`** (生产环境)
```
VITE_API_BASE_URL=https://api.example.com
VITE_WS_URL=wss://api.example.com/ws
```

## 🚀 开发环境部署

### 第一步：启动基础设施
```bash
make up
```
启动 MySQL、Redis、NATS、Etcd、Prometheus、Grafana

### 第二步：启动后端服务
```bash
# 终端 1
make dev-gateway

# 终端 2
make dev-logic

# 终端 3
make dev-task
```

**验证：**
- Gateway HTTP: `http://localhost:8080/health`
- Logic gRPC: `grpcurl -plaintext localhost:9090 list`
- Task 日志：查看消费者是否启动

### 第三步：启动前端
```bash
make web-dev
```
访问：`http://localhost:5173`

## 🐳 生产环境部署

### 第一步：构建 Docker 镜像
```bash
make build-docker-all
```

### 第二步：配置反向代理（Nginx）

```nginx
upstream gateway_http {
    server gateway:8080;
}

upstream gateway_ws {
    server gateway:8081;
}

server {
    listen 80;
    server_name api.example.com;

    # HTTP API
    location / {
        proxy_pass http://gateway_http;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    # WebSocket
    location /ws {
        proxy_pass http://gateway_ws;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### 第三步：启动容器
```bash
docker compose up -d
```

## 📍 地址速查表

| 服务 | 内部地址 | 外部地址 | 用途 |
|------|---------|---------|------|
| Gateway HTTP | `:8080` | `https://api.example.com` | 客户端 API 调用 |
| Gateway WebSocket | `:8081` | `wss://api.example.com/ws` | 实时消息推送 |
| Gateway gRPC | `:9091` | ❌ 不暴露 | Task 推送消息 |
| Logic gRPC | `:9090` | ❌ 不暴露 | Gateway 业务处理 |
| Task | - | ❌ 不暴露 | 内部异步任务 |

## 🔐 安全建议

1. **内部服务通信**
   - 使用内网访问（Docker 网络或私有子网）
   - 不要暴露 gRPC 端口到公网

2. **外部服务暴露**
   - 使用 HTTPS/WSS 加密
   - 通过反向代理（Nginx/HAProxy）对外暴露
   - 配置速率限制和 DDoS 防护

3. **环境变量管理**
   - 生产环境不要提交 `.env` 文件
   - 使用 Docker secrets 或 Kubernetes secrets
   - 前端 `VITE_` 变量会被编译到构建产物中，注意不要泄露敏感信息

## 📖 常见问题

### Q: 为什么 Logic 和 Task 不对外暴露？
A: 它们是内部服务，只被 Gateway 和 Task 相互调用。对外统一通过 Gateway 提供服务，便于版本控制、限流、认证等。

### Q: 如何在不同环境切换配置？
A: 通过 `RESONANCE_ENV` 环境变量：
```bash
RESONANCE_ENV=dev go run main.go -module gateway    # 加载 gateway.dev.yaml
RESONANCE_ENV=prod go run main.go -module gateway   # 加载 gateway.prod.yaml
```

### Q: 前端如何知道 Gateway 地址？
A: 通过环境变量配置：
```bash
VITE_API_BASE_URL=http://api.example.com make web-build
```
或在 `web/.env.production` 中配置。

### Q: 能否在同一台机器运行多个 Gateway 实例？
A: 可以，但需要修改 `http_addr` 和 `ws_addr` 使用不同端口，然后通过负载均衡器分流。

## 🔗 相关文件

- [AGENTS.md](./AGENTS.md) - 后端开发规范
- [web/AGENTS.md](./web/AGENTS.md) - 前端开发规范
- [api/ARCHITECTURE.md](./api/ARCHITECTURE.md) - API 架构详解
