# Resonance 开发路线图

## 本地前置依赖

```bash
make install-gen   # 幂等安装代码生成器（版本锁定，纯本地、离线可跑）
```

- Go `1.26.10`、Node `22`、`buf` `1.66.1`。
- `protoc-gen-go` / `protoc-gen-go-grpc` / `protoc-gen-connect-go` 由 `go install` 装到
  `$(go env GOPATH)/bin`，`protoc-gen-es` 由 `tools/package.json` 锁定并装在
  `tools/node_modules/.bin`，`make gen` 会自动把两处加入 PATH。
- 版本与 `api/buf.gen.*.yaml`、`go.mod`、`web/package.json` 一一对应；插件全部是
  `local:`，不使用 BSR 远端插件，`make gen` 不依赖网络配额与登录状态。

## 已有 IM 能力

- [x] ChatEvent + Outbox/NATS/Inbox 消息链路
- [x] 消息撤回、编辑、离线同步与多端已读
- [x] Gateway 接入、Logic 业务裁决、Task 写扩散

## 当前：精简 Pilot

产品只提供一个系统 Bot；每个用户在当前租户只有一个固定私聊，一直连续对话。

- [x] Genesis 升级为 `v1.0.0`，Go/CI/Docker 对齐 `1.26.10`
- [x] 无参 `CreateAgentSession` 幂等返回唯一 Pilot 私聊
- [x] owner 部分唯一索引，不以模型/Prompt 版本创建新会话
- [x] 注册、登录和列表幂等 provisioning；禁止普通建聊添加 Bot
- [x] 私聊只允许一个真人和 Bot，禁止加入第三人
- [x] 一个 Pilot 服务处理所有租户，按现有 IM 会话边界隔离
- [x] 保留 Pi 独立 Session、上下文压缩、历史失效重建
- [x] 保留 durable Run、源事件去重、会话串行与最终回复幂等
- [x] 保留 WS 文本增量、最终 ChatEvent 与 Inbox 恢复
- [x] 删除 IAM 助手、Mutation、审批服务/协议/前端
- [x] 删除 Tool Broker、Capability、动态工具和租户预算账本
- [x] 删除多 Profile、远程 Runtime/Relay、双镜像与自研出口代理
- [x] 单 Pilot 容器内运行固定版本 Pi 子进程，最小环境与非 root
- [x] 删除 RC2 专用证据流水线，保留正常编译/单测/协议/前端门禁
- [x] 仅支持全新数据初始化，删除旧库迁移脚本与历史用户回填

## 后续按需，不作为本轮交付承诺

- [ ] 生成状态查询与前端显式取消入口
- [ ] 候选环境真实模型多轮/断线/重启验收
- [ ] 有真实业务需求后再设计窄只读工具；不恢复通用 IAM 运维

设计依据：[Pilot](docs/14-ai-service.md)、[Pi 接入](docs/15-agent-harness.md)、[运行维护](docs/16-agent-operations.md)。
