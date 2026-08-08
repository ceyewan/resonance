# ADR-001：Pilot 采用 Go 控制面 + Pi Runtime

- 状态：Accepted
- 日期：2026-08-08
- 决策人：Resonance Maintainers
- 关联文档：`../14-ai-service.md`、`../15-agent-harness.md`

## 背景

Resonance 需要新增 AI Agent 微服务：普通用户可以聊天并查询自己的信息，IAM 管理员可以查询和执行受控管理操作。主语言是 Go，同时要求自定义 Tool、多租户隔离、审批、审计，以及与现有 ChatEvent/Outbox/Inbox 链路集成。

直接在 Go 中实现 Agent Loop 并不难做出 Demo，但生产化还要持续维护 Session、上下文压缩、Provider 差异、Tool Loop、流式事件、重试、取消和恢复。另一方面，采用完整 Agent 平台又会与现有 Go 服务、IAM 和会话控制面重叠。

## 决策

采用以下职责划分：

- Pilot 使用 Go 实现业务控制面和 Runtime Supervisor。
- Pi 通过本地 stdin/stdout JSONL RPC 提供 Harness Runtime。
- 每个 Active Run 使用独立 Pi 子进程和 staging Session。
- 自定义 Tool 通过唯一可信 TypeScript Bridge 转发到 Go Tool Broker。
- Actor、Tenant、Role/Scope、审批、幂等和审计全部由 Go 执行。
- Pi 不开放公网或内部 HTTP 端口，不成为系统接入层。
- Pilot 通过 `AgentRuntime` 接口隔离 Pi，保留未来替换 Runtime 的能力。

## 关键配套决策

1. Pi 的 coding prompt、内建文件/shell Tool 和资源发现全部关闭。
2. Session 使用 prepare-then-commit：先冻结候选 Session 与最终输出，再幂等写 Logic，最后 CAS 更新 Binding。
3. IAM 写操作使用 durable two-phase approval，不让 Pi 进程跨小时等待。
4. 普通个人 AI 会话首版限制为一个真人 Actor 加一个 Bot，防止共享 Session 泄露 self-scoped Tool 结果。
5. 当前代码没有 Tenant 和系统管理员 Role，完成前只能做单租户只读 POC。
6. Logic 权威持有审批事实，Pilot/Tool Broker 权威持有冻结参数和执行事实；两者通过 `call_id + args_hash` 幂等收敛，不做跨库事务。

## 替代方案

### Go 自研 Harness

优点是完全 Go 原生、控制力最高。缺点是需要自行维护 Agent Loop、Session、Compaction、Provider 重试和工具状态，首版投入与长期维护面过大，因此拒绝作为首选。

### Docker Agent

Docker Agent 本身是带 Session、API、Tool 和权限的完整 Agent 框架，不只是 Docker 隔离层。使用它会与 Resonance Go 控制面形成职责重叠，因此拒绝。Docker 容器仍用于隔离 Pi。

### Codex App Server

协议和 Thread/Approval 能力完整，但更偏编码 Agent；官方当前仍把 App Server/WebSocket 标为实验性且不支持生产工作负载。保留为编码场景候选，不作为 IAM 助手首选。

### Claude Agent / 直接模型 SDK

Claude 及其他 Provider SDK 可以提供 Go Tool Runner 或基础 Tool Loop，但多轮 Session、持久审批和跨 Provider Runtime 仍需要控制面处理。用户当前也不选择 PaaS，因此只作为退出路径。

## 后果

### 正面

- 避免重写 Harness 的复杂运行能力。
- IAM、多租户和审计继续由 Go 领域代码掌握。
- Pi 进程边界清楚，可以独立限额、升级和回滚。
- Runtime-neutral 接口降低未来替换成本。

### 负面

- 需要维护一层严格 JSONL RPC Adapter。
- 自定义 Tool 仍有一个小型 TypeScript Bridge。
- Pi 是 Coding Harness，必须覆盖 Prompt、Compaction 和默认工具。
- Pi Session 是本地 JSONL，需要额外的快照、版本、Rollover 和多实例存储方案。
- Pi 迭代较快，升级需要协议与 Session 契约测试。

## 重新评估触发条件

出现以下任一情况时应新增 ADR 重新评估：

- Pi 无法稳定支持非编码业务 Prompt 或关键 Provider。
- RPC/Session 频繁 breaking，维护 Adapter 的成本超过自研或替代 Runtime。
- Per-run 子进程的性能无法通过容量规划接受。
- 官方 Go Agent Runtime 提供等价 Session、Compaction、HITL 和运维能力。
- 产品转向编码 Agent、多 Agent 或托管 Agent 平台。

## 参考资料

- [Pi RPC Mode](https://pi.dev/docs/latest/rpc)
- [Pi Security](https://pi.dev/docs/latest/security)
- [Pi Containerization](https://pi.dev/docs/latest/containerization)
- [Pi Extensions](https://pi.dev/docs/latest/extensions)
- [Codex App Server](https://developers.openai.com/codex/app-server/)
