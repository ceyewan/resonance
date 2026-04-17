# 📡 Resonance API 模块

本项目使用 [Buf](https://buf.build/) 管理 Protobuf 协议，并通过 [ConnectRPC](https://connectrpc.com/) 和原生 gRPC 提供双协议支持。

## 📖 文档说明

- **[ARCHITECTURE.md](./ARCHITECTURE.md)**: 详细的架构设计、服务分层、调用关系及协议决策。
- **本 README**: 快速上手指南、开发命令及调用示例。

## ⚙️ 环境准备

本项目使用 [Buf](https://buf.build/) 作为协议管理工具。得益于 Buf 的 **Remote Plugins** 模式，你**不需要**在本地安装 `protoc` 或任何 Go/TS 的插件，只需安装 `buf` CLI 即可。

### 1. 安装 Buf CLI

- **macOS (Homebrew)**:

    ```bash
    brew install bufbuild/buf/buf
    ```

### 2. 验证安装

```bash
buf --version
```

## 🛠️ 快速上手

### 1. 生成代码

```bash
make gen
```

该命令会顺序执行 3 次 `buf generate`（Go、Connect-Go、TypeScript）。命令每次都会执行，但仅当输入或插件版本变化时，生成文件内容才会发生变更。

### 2. 目录结构

- `proto/`: Protobuf 定义文件
- `gen/`: 生成的代码（Go & TS）
- `buf.yaml`: Buf 模块配置
- `buf.gen.*.yaml`: 各语言的生成插件配置

## 📡 调用示例 (ConnectRPC)

对外的 Gateway API 支持 HTTP/1.1 + JSON 访问，对前端极其友好。

### TypeScript 客户端

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@/gen/gateway/v1/api_connect";

const transport = createConnectTransport({
    baseUrl: "http://localhost:8080",
});

const client = createPromiseClient(AuthService, transport);
const response = await client.login({ username: "...", password: "..." });
```

`web` 项目通常通过 `src/gen -> ../../api/gen/ts` 的软链接（或等价 alias）来引用生成代码。

### Curl 模拟

```bash
curl -X POST http://localhost:8080/resonance.gateway.v1.AuthService/Login \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "..."}'
```

## ⚠️ 开发注意事项

1. **版本锁定**: 插件版本与 `go.mod` 及 `web/package.json` 强绑定。升级依赖库时，请同步更新 `buf.gen.*.yaml` 中的插件版本。
2. **破坏性检查**: 提交协议变更前，建议运行 `buf breaking --against '.git#branch=main'` 检查。
