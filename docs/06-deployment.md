# 部署

## 服务与依赖

```text
PostgreSQL / Redis / NATS / etcd
          ↑
Gateway — Logic — Task — Web
             ↑
           Pilot → Pi → Provider
```

`main.go -module` 支持 init、gateway、logic、task、web、pilot。
IM 使用 `deploy/Dockerfile` 的 `final` 镜像；Pilot 使用 `pilot-final`，一个容器内运行 Go + 固定 Node/Pi。
不再部署 IAM Pilot、Runtime sidecar、私有 RPC Relay 或出口代理。

## 本地

1. `cp .env.example .env`，设置独立的 JWT/Gateway/Pilot 签名密钥和模型凭证。
2. `make up` 启动；后续使用 `make update-local` 重建并移除孤儿服务。
3. Web 默认 `http://localhost:4173`，Gateway 默认 `http://localhost:8080`。

只需要基础设施时执行 `make up-infra`。开发端可独立启动 Go 服务与 Vite。
Pilot 的 Pi binary/extension/work/agent/session 路径必须是可信绝对路径；在宿主机运行需覆盖容器内默认路径。

## 生产

使用 `deploy/base.yaml + deploy/services.yaml + deploy/services.prod.yaml`；Gateway/Web 经 Caddy 反代，不暴露宿主业务端口。
`deploy/scripts/deploy-production.sh` 校验独立密钥、Provider HTTPS endpoint 和 Pilot digest 后部署。
模型 Key 只显式注入 Pilot，不使用公共 env_file。Pi 子进程再通过最小白名单取模型配置。

Pilot 非 root、只读 rootfs、移除 capabilities、限制 PID/CPU/内存及临时磁盘，只挂载 `pilot-data`。
Pi 的运行时配置和 Session 位于该私有卷；不挂载 Docker Socket、宿主 Home 或开发者 `~/.pi`。

当前支持单 Pilot 实例。本地数据卷不等于跨主机高可用；扩容前再引入共享 Session 存储，不预先保留双 Runtime 架构。

## 端口

| 服务 | 端口 | 用途 |
| --- | --- | --- |
| Gateway | 8080 / 15092 | HTTP/WS / 内部 Push gRPC |
| Logic | 15090 / 15091 | gRPC / health |
| Pilot | 15093 | health |
| Web | 4173 | 静态页面与 runtime-config |
| Metrics | 9090–9093 | Task/Logic/Gateway/Pilot |

可观测性栈可通过 `make up-observability` 在独立 Compose project 启动。
旧库的显式迁移与回滚原则见 [16-agent-operations.md](16-agent-operations.md)。
