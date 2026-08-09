# Loop 01 验证记录：Pi Runtime Adapter

> 本文保留首轮当时的证据快照；启动/取消/Probe/Session 安全复审后的最新结论见 `03-pi-runtime-hardening.md`。

- 日期：2026-08-08
- 范围：`pilot/runtime`、`pilot/runtime/pi`
- 目标：在不依赖 Node、真实 Pi、Provider Key 或 Docker 的条件下，证明 Runtime 协议与子进程生命周期基础可被确定性测试。
- 结论：本切片通过；后续已在 `13-agent-runtime-isolation.md` 补齐固定镜像、真实 Pi+Bridge readiness 与隔离部署契约。

## 1. 本轮验收条件

| 条件 | 证据 | 结果 |
| ---- | ---- | ---- |
| Runtime 上层不依赖 Pi 类型 | `pilot/runtime/runtime.go` 与编译期接口断言 | 通过 |
| 只按 LF 分帧，兼容 CRLF 和无尾 LF | `TestDecoder_CRLFAndFinalFrameWithoutLF` | 通过 |
| 大于默认 Scanner 64 KiB 的 frame 可解析 | `TestDecoder_FrameLargerThanScannerLimit` | 通过 |
| 任意分片和 UTF-8/U+2028/U+2029 不破坏 framing | `TestDecoder_FragmentedUTF8AndUnicodeSeparators` | 通过 |
| malformed JSON、普通 stdout 文本和资源超限 fail closed | Decoder limit/pollution 测试 | 通过 |
| command response 按 ID/command 相关，允许与 Event 交错 | `TestRPCClient_CommandAndEventsInterleave` | 通过 |
| Prompt ACK 不等于 Run 完成，未知非关键事件向前兼容 | `TestRPCClient_PromptAckDoesNotSettleAndUnknownEventSurvives` | 通过 |
| 事件消费者阻塞时内存保持有界 | `TestRPCClient_EventBackpressureFailsBoundedly` | 通过 |
| `agent_end` 不结算，只有 `agent_settled` 触发权威结果查询 | Mapper 与 `TestPiRuntime_SettledReturnsAuthoritativeFinalText` | 通过 |
| 最终文本来自 `get_last_assistant_text`，不由 Delta 拼接 | 同上，Fake 故意返回不完整 Delta | 通过 |
| Provider、Model、Session 不匹配时 fail closed | `verifyState` / `verifySettledSession` | 代码约束；异常分支专项测试待补 |
| 内建工具和资源发现启动参数固定关闭 | Fake Process 对完整安全 Flag 集断言 | 通过 |
| Capability 只进入显式 child env，不继承宿主环境 | ProcessSpec 测试 | 通过 |
| Abort 优先使用 RPC，忽略 Abort 时升级 TERM/KILL | graceful/cancel escalation 测试 | 通过 |
| stderr 超限后仍持续排空且只保存有界内容 | `TestPiRuntime_StderrFloodIsTruncatedButDrained` | 通过 |
| 每个进程只 `Wait()` 一次，结果可重复读取 | Fake Process 计数与重复 `EventStream.Wait` | 通过 |
| Pi 版本可以做精确 Probe | `TestPiRuntime_ProbePinsExactVersion` | 通过 |

## 2. 可复现命令

受当前 workspace sandbox 限制，Go 和 golangci-lint cache 显式放在 `/tmp`：

```bash
env GOCACHE=/tmp/resonance-gocache go test ./pilot/runtime/... -count=1
env GOCACHE=/tmp/resonance-gocache go test -race ./pilot/runtime/pi -count=20
env GOCACHE=/tmp/resonance-gocache go vet ./pilot/runtime/...
env GOCACHE=/tmp/resonance-gocache \
  GOLANGCI_LINT_CACHE=/tmp/resonance-golangci-cache \
  golangci-lint run --allow-parallel-runners --config .golangci.yaml ./pilot/...
```

本轮实测结果：上述命令全部通过；Race 测试重复 20 次通过。

## 3. 安全与一致性说明

- stdout 协议错误和 Extension Error 只在错误中保留长度与 SHA-256 前缀，不回显可能含 Secret/PII 的原文。
- stderr 达到保存上限后继续读取并丢弃，避免子进程因 pipe 填满死锁。
- 调用方 Context 不传给 `exec.CommandContext`；取消走 `abort → close stdin → SIGTERM → SIGKILL → Wait`。
- `agent_settled` 到达后才调用 `get_last_assistant_text`、`get_session_stats` 和 `get_entries`，三者全部成功后才向上层发 `Settled`。
- Pi Session File 必须保持在本次 staging Session Directory 内，并在结算前后保持同一 Session ID/File。

## 4. 未覆盖与下一轮输入

以下项目不属于本轮已经证明的结论：

- 当前开发机未安装真实 Pi；需要在固定版本镜像中运行 live contract suite。
- Pi `get_state` 不返回 Tool 列表；必须实现可信 Bridge readiness/Manifest Hash 握手。
- staging 临时目录仍由 Session Manager 创建、提交和删除；Adapter 只负责验证路径和回收进程/Pipe，该职责边界没有改变。
- 后续切片已接通 Pilot 服务入口、Run/MQ/租约、Session Binding 与 Pilot→Logic 最终提交；本文件只保留首个 Adapter 切片的证据。
- 还需要补充取消与 settled 高频竞态、异常 Session、进程提前退出以及真实 Pi Session Resume/Compaction fixture。

后续纵向切片应建立 `t_agent_run` + Fake Runtime + Logic 最终提交闭环，证明 MQ 重投和提交响应丢失会复用 Loop 02 的原 ACK、不会生成重复 Bot Message，再把真实 Pi Adapter 接入该闭环。
