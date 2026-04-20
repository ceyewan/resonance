# 📡 Resonance API 模块

本项目使用 [Buf](https://buf.build/) 管理 Protobuf 协议，对内服务间调用走原生 gRPC，对外面向 Web/移动端走 [Connect](https://connectrpc.com/) 协议。

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

该命令会顺序执行 3 次 `buf generate`：

1. `buf.gen.go.yaml`：为所有 proto 生成 Go message + 原生 gRPC stub
2. `buf.gen.connect.yaml`：仅为 `gateway/v1/auth.proto` 与 `gateway/v1/session.proto` 生成 Connect-Go handler（对外）
3. `buf.gen.ts.yaml`：为 `gateway/v1/{auth,session,packet}.proto` 和 `common/*` 生成 TypeScript 代码

命令每次都会执行，但仅当输入或插件版本变化时，生成文件内容才会发生变更。

### 2. 目录结构

- `proto/`: Protobuf 定义文件
- `gen/go/`: 生成的 Go 代码（message + gRPC + Connect-Go handler）
- `gen/ts/`: 生成的 TypeScript 代码（Connect-ES v2，Service 定义与 message 同在 `*_pb.ts`）
- `buf.yaml`: Buf 模块配置
- `buf.gen.*.yaml`: 各语言的生成插件配置

## 📡 调用示例 (Connect)

对外的 Gateway API 支持 HTTP/1.1 + JSON 访问，对前端极其友好。

### TypeScript 客户端（Connect-ES v2）

Connect-ES v2 起 `Service` 定义已并入 `*_pb.ts`，不再生成独立的 `*_connect.ts`：

```typescript
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@gen/gateway/v1/auth_pb";

const transport = createConnectTransport({
    baseUrl: "http://localhost:8080",
});

const client = createClient(AuthService, transport);
const response = await client.login({ username: "...", password: "..." });
```

`web` 项目通过 `src/gen -> ../../api/gen/ts` 的软链接（搭配 `@gen/*` alias）引用生成代码。

### Curl 模拟

```bash
curl -X POST http://localhost:8080/resonance.gateway.v1.AuthService/Login \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "..."}'
```

## ⚠️ 开发注意事项

1. **版本锁定**：插件版本与 `go.mod` 及 `web/package.json` 强绑定。升级运行时库时，请同步更新 `buf.gen.*.yaml` 中的插件版本。当前基线：

    | 维度 | 版本 |
    |------|------|
    | Go runtime | `google.golang.org/protobuf v1.36.11` / `grpc v1.79.3` / `connectrpc.com/connect v1.19.1` |
    | Go 插件 | `protocolbuffers/go:v1.36.11` / `grpc/go:v1.5.1` / `connectrpc/go:v1.19.1` |
    | TS runtime | `@bufbuild/protobuf@2.11.0` / `@connectrpc/connect@2.1.1` / `@connectrpc/connect-web@2.1.1` |
    | TS 插件 | `bufbuild/es:v2.11.0`（v2 已并吞 `connectrpc/es`） |

2. **破坏性检查**：提交协议变更前，建议运行 `buf breaking --against '.git#branch=main'` 检查。
