# Pi Runtime 接入

Pilot 复用 Pi，不自研 Agent Loop。Go 通过 stdin/stdout JSONL RPC 驱动固定版本 Pi，目前为 `0.84.1`；依赖由 `pilot/bridge/package-lock.json` 固定。

## 模块

| 模块 | 职责 |
| --- | --- |
| `pilot/ingress` | 校验一真人一 Bot 的 IM 会话并持久入队 |
| `pilot/coordinator` | 会话串行、Worker 并发、租约恢复、冻结结果后提交 |
| `pilot/runtime/pi` | 进程、RPC framing、版本探针、Abort、超时与回收 |
| `pilot/session` | 私有 staging、已提交快照、失效重建、容量与 GC |
| `pilot/logicclient` | gRPC 读取有效历史、幂等提交最终消息 |
| `pilot/stream` | 合并文本增量，经有界队列发布临时流 |
| `pilot/bridge` | 注册平台模型和聊天摘要逻辑，不注册工具 |

只有一个服务端助手配置。内部 Run/Session 的 `profile_id/profile_version` 只是冻结的 Runtime 配置标识，不是客户端可选的角色或第二个会话命名空间。

## 启动约束

Pi 使用 `--mode rpc`，关闭 builtin tools、扩展发现、Skills、Prompt Templates、Context Files、Themes 和项目授权。
完整替换 coding system prompt，禁止用户选择扩展、目录或模型凭证。

启动时精确核对 Pi 版本；Prompt 前通过 `get_state` 核对模型和 Session，通过 `get_commands` 核对可信扩展 readiness（`tool_count=0`）。未知扩展命令在生成前失败。

## 完成与恢复

- 等待 `agent_settled`，不能把中间 `agent_end` 当作最终完成。
- 持续读取 stdout；协议帧、总输出、stderr 与事件队列均有上限。
- 超时/取消先 Abort，再按 TERM/KILL 回收，始终 Wait 进程。
- 成功后先冻结最终文本和候选 Session，再提交 Logic；响应丢失只重试冻结结果。
- 最终回复已入库但 ACK 丢失，由固定 `client_msg_id` 返回首次结果，不再生成第二条。
- 失败 staging 不进入下一轮；历史 Edit/Recall 使已提交 Session dirty，防止旧正文残留。

## 凭证与限制

Pi 仅收到 `PATH/HOME/PI_OFFLINE`、平台 Provider 配置和每次输出 Token 上限。不得继承 Pilot 的整个环境。
当前模型扩展使用 DashScope OpenAI-compatible HTTPS endpoint；没有任意 HTTP、文件、Shell 或业务工具。

平台费用、企业配额和管理员授权不属于本 Runtime。未来新增工具须重新设计其最小权限边界，不能重新开放内建 coding 工具。
