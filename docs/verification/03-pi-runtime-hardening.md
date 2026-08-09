# Loop 03 验证记录：Pi Runtime 安全与生命周期加固

- 日期：2026-08-08
- 范围：`pilot/runtime`、`pilot/runtime/pi`
- 输入：Loop 01 的独立只读复审，包含 9 项上线前 P1 与协议/测试 P2。
- 结论：复审中的进程/取消/Probe/RuntimeEvent/Session 路径 P1 已在 Fake 与真实 OS helper 层闭环；后续固定 Pi 镜像、Bridge readiness 和容器/网络隔离见 `13-agent-runtime-isolation.md`。

## 1. 已修复的上线风险

| 风险 | 修复与证据 |
| ---- | ---------- |
| Start/handshake 失败时并发 Abort 永久等待 | reserve 后统一 Run 生命周期；Start/get_state/Prompt 三阶段并发 Abort 有界完成 |
| Event 洪泛饿死取消 | monitor 每处理一个 Event 前优先检查 Abort/Context/processDone；持续 flood 测试在 1 秒内完成 |
| Prompt 已写但 ACK 超时被当成未发送 | `CommandOutcomeUnknownError` 区分未写与已写；unknown 路径用独立 Context 发 Abort并等待 settled/grace |
| ACK 前 Event 塞满 RPC 队列 | 独立、有界 startup collector 持续消费；`queue + 1` burst 后 Prompt ACK 仍可分发 |
| Tool 参数/结果泄漏到上层 | Runtime-neutral `ToolEvent` 移除 Raw Args/Result；含 API Key/SSN fixture 的 RuntimeEvent/错误中不出现原文 |
| Session symlink 越界 | 解析可信 root，要求私有目录；逐段 `Lstat` 拒绝 file/parent symlink，handshake 与 settled 再核验 |
| 版本固定依赖外部自律 | `ExpectedVersion` 必填；成功 Probe 前 Run 返回 `ErrRuntimeNotReady`；Shutdown 后永久拒绝 Run |
| Probe stdout flood/不退出死锁 | 内部 hard timeout；stdout/stderr 持续 drain 到 bounded capture；超限 Kill + Wait；错误只含长度/Hash |
| 服务关闭无法回收全部进程 | `AgentRuntime.Shutdown(ctx)` 快照活动 Run、发起不可撤销的内部 Abort，并等待 cleanup/reap |

同时补齐：CRLF 下恰好达到 frame limit、known event required fields、response success/id/command、read command required bool/counter/usage、partial stdin write outcome unknown、Failed 终态脱敏事件和危险 Node env 拒绝。

## 2. 可复现命令

```bash
env GOCACHE=/tmp/resonance-gocache \
  go test ./pilot/runtime/... -count=1

env GOCACHE=/tmp/resonance-gocache \
  go test -race ./pilot/runtime/pi -count=20

env GOCACHE=/tmp/resonance-gocache \
  go test -race ./pilot/runtime/pi \
  -run 'TestPiRuntime_StderrFloodIsTruncatedButDrained' -count=100

env GOCACHE=/tmp/resonance-gocache go vet ./pilot/runtime/...

env GOCACHE=/tmp/resonance-gocache \
  GOLANGCI_LINT_CACHE=/tmp/resonance-golangci-cache \
  golangci-lint run --allow-parallel-runners --config .golangci.yaml ./pilot/...

env GOCACHE=/tmp/resonance-gocache go test ./... -count=1
```

上述命令全部通过。Race 初次重复执行发现 stderr Fake 脚本在写满前主动 `exit` 的测试竞态；修正为先完成 1 MiB 写入再执行正常协议后，专项 100 次与整套 20 次 Race 均通过。

## 3. 真实进程边界

测试二进制通过 helper-process 模式自重启，不依赖 Node 或 Pi，真实覆盖：

- 显式 child env 和 WorkDir；
- stdin 写入/关闭、stdout/stderr 并发读取；
- `Setpgid` 后向整个进程组发送 SIGTERM；
- 非零 signal exit；
- `Wait()` 完成后 Signal 幂等无害。

这比纯 `io.Pipe` Fake 多证明了 `os/exec` 和操作系统 Pipe/信号行为，但仍不等于真实 Pi 协议契约。

## 4. 剩余边界

- 固定 Pi 0.84.1 已执行真实 `get_state/get_commands/get_session_stats/Abort` 与 Bridge Manifest readiness 离线契约；带 Provider 的 Resume/Compaction 仍属于候选版本 Canary。
- Session 路径检查能拒绝传入和运行时返回的 symlink；但 Pi 与其他同 UID 进程之间的恶意 TOCTOU 只能靠 run-private volume、容器/OS sandbox 和最小文件系统挂载进一步隔离，Go 无法把 CLI 的路径参数替换成 `openat(O_NOFOLLOW)` 文件描述符。
- `ExpectedVersion`、Node/Pi lockfile、control/runtime 镜像 target 与镜像内版本检查均已成为 CI 门禁。
- Startup collector 是有界的；异常 Runtime 在 ACK 前超过限制会明确失败，不保证保留所有 ephemeral 进度。
- Tool 原始数据不进入 RuntimeEvent；Tool Broker 已独立实现冻结参数安全引用、脱敏摘要和审计，不能重新把 Raw JSON 塞回通用事件。
