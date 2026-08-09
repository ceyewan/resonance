# Resonance 部署指南

本目录提供 Resonance 的 Docker Compose 部署配置，支持本地全 Docker 启动和生产环境部署。

## 文件说明

- `base.yaml`：基础设施服务，包含 PostgreSQL、Redis、NATS、Etcd
- `services.yaml`：业务服务，包含 `init`、`logic`、`task`、`gateway`、`web`，以及两个 Agent Profile 的 control/runtime 和 Provider Egress Proxy
- `services.local.yaml`：仅本地开发覆盖，显式启用 Docker Desktop/VPN 的 RFC 2544 DNS 合成地址兼容
- `services.prod.yaml`：生产覆盖，关闭业务端口暴露并接入 Caddy
- `Dockerfile`：统一镜像构建文件
- `scripts/deploy-local.sh`：本地全 Docker 启动脚本
- `scripts/deploy-production.sh`：生产部署脚本
- `scripts/rollback-agent.sh`：Agent control/runtime 不变 digest 组合校验与回滚
- `scripts/build-push.sh`：镜像构建与推送脚本

## 前置条件

- 已安装 Docker 和 Docker Compose
- 项目根目录下已创建 `.env`

初始化配置：

```bash
cp .env.example .env
```

## 本地部署

启动整套服务：

```bash
make up
```

查看日志：

```bash
make logs
```

停止服务：

```bash
make down
```

默认访问地址：

- Web：`http://localhost:4173`
- Gateway：`http://localhost:8080`

## 生产部署

生产模式依赖宿主机上的 Caddy Docker Proxy，并要求 Docker 网络 `caddy` 已存在。

启动前至少确认 `.env` 中已设置：

- `CADDY_GATEWAY_DOMAIN`
- `CADDY_WEB_DOMAIN`
- `RESONANCE_AUTH_SECRET_KEY`
- `RESONANCE_POSTGRES_PASSWORD`
- `RESONANCE_ADMIN_PASSWORD`
- `RESONANCE_GATEWAY_SERVICE_AUTH_SECRET`
- 两个 Profile 各自独立的 Capability/service-auth Secret
- `DASHSCOPE_API_KEY`
- `DASHSCOPE_BASE_URL`
- `DASHSCOPE_MODEL`
- `RESONANCE_PILOT_IMAGE=repository@sha256:<64 hex>`
- `RESONANCE_PILOT_RUNTIME_IMAGE=repository@sha256:<64 hex>`

主应用可按 tag 部署，Agent control/runtime 不论主应用 tag 为何都必须使用同一发布产物记录的不变 digest 组合：

```bash
make up-prod
```

或指定镜像标签：

```bash
./deploy/scripts/deploy-production.sh v0.1.0
```

GitHub 发布工作流会生成 `agent-release-<tag>` artifact，记录两个 digest、Pi/Bridge/Remote Runtime 协议版本和源码 SHA；两个镜像同时发布 SBOM 与 provenance attestation。该组合必须成对保留。回滚时先只做校验：

```bash
PILOT_PREVIOUS_IMAGE_DIGEST='registry/resonance-pilot@sha256:<64 hex>' \
PILOT_RUNTIME_PREVIOUS_IMAGE_DIGEST='registry/resonance-pilot-runtime@sha256:<64 hex>' \
./deploy/scripts/rollback-agent.sh --validate-only
```

实际操作必须显式改为 `--execute`。脚本先停 Pilot ingress，再恢复两个 Runtime 并等待健康，最后恢复两个 control 并核对实际镜像引用。脚本会生成不覆盖的 `0600` JSON 证据；恢复更广的 Tenant admission 前仍必须记录已有旧 Session fixture 的实际恢复结果。

查看生产日志：

```bash
make logs-prod
```

停止生产服务：

```bash
make down-prod
```

## 镜像构建

构建本地镜像：

```bash
./deploy/scripts/build-push.sh local
```

构建并推送指定标签：

```bash
./deploy/scripts/build-push.sh push v0.1.0
```

## 配置说明

- 服务默认读取 `configs/{logic,gateway,task,web}.yaml`
- 环境差异通过 `.env` 覆盖，不再使用 `*.prod.yaml`
- `init` 是一次性初始化任务，会随 Compose 启动链自动执行
- 生产环境下 `gateway` 和 `web` 不直接暴露宿主机端口
- Docker 模式下 `logic` / `gateway` 额外通过 `RESONANCE_SERVICE_HOST=<container-hostname>` 覆盖 `configs/*.yaml` 里的 `localhost`，保证 etcd 中注册的是容器内可达地址，而不是回环地址

### 前端运行时配置

Web 镜像**一次构建、多环境复用**，不把 API / WebSocket 地址打进 bundle。

- `.env` 里设置 `RESONANCE_WEB_API_BASE_URL` / `RESONANCE_WEB_WS_BASE_URL`
- 本地 Docker 模式下，`services.yaml` 默认注入：

    ```env
    RESONANCE_WEB_API_BASE_URL=http://localhost:8080
    RESONANCE_WEB_WS_BASE_URL=ws://localhost:8080/ws
    ```

  这样浏览器访问 `http://localhost:4173` 时，请求会直接打到宿主机暴露的 Gateway，而不是误打到静态 Web 服务自身。
- `services.prod.yaml` 注入到 `resonance-web` 容器
- 容器内 `webserver` 模块响应 `GET /runtime-config.js`，返回：

    ```js
    window.__RESONANCE_RUNTIME_CONFIG__ = {
      apiBaseUrl: "...",
      wsBaseUrl: "...",
    };
    ```

- 前端 `index.html` 同步加载该脚本，`transport.ts` / WS 客户端读取 `window.__RESONANCE_RUNTIME_CONFIG__` 构造请求。
- 生产模式下留空时 apiBaseUrl = wsBaseUrl = ""，前端走**同源**调用（由 Caddy 反代到 gateway）。
- 开发期 `vite dev` 不走这条链路，由 `vite.config.ts` 的 dev middleware + `server.proxy` 兜底，直接把 `/resonance.*` 和 `/ws` 代理到 `localhost:8080`。

详见 `docs/frontend/01-web-architecture.md § 2.1`。

## 常用命令

```bash
make up          # 本地全 Docker 启动
make update-local # 重新构建并更新本地 Docker 部署
make down        # 停止本地服务
make logs        # 查看本地日志
make up-prod     # 生产部署
make down-prod   # 停止生产服务
make logs-prod   # 查看生产日志
```
