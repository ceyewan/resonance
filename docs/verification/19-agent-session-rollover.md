# Agent Session Rollover 验证

日期：2026-08-09

## 目标

证明 Pi compaction 不能造成 Session JSONL 在磁盘上无限增长。Pilot 必须在硬快照
上限前同时按字节和 entry 数触发安全 rollover，且不能解析或手改不稳定的 Pi JSONL
字段。

## 实现边界

Session Store 配置三个独立限制：

- `max_snapshot_bytes`：Candidate 可持久化的硬上限，默认 64 MiB；
- `rollover_bytes`：已提交快照的软字节阈值，默认 32 MiB；
- `rollover_entry_count`：按 LF framing 统计的软 entry 阈值，默认 20000。

Candidate 准备在受控临时文件上计算 SHA-256、字节数和 entry 数，并把容量写入
AgentRun；Session CAS 后同步写入 Binding。下一轮恢复仍从不可变对象重新核验
checksum 和真实计数，不相信数据库字段替代文件完整性检查。

达到任一软阈值时，Store 同时返回 `ErrSessionRollover` 与
`ErrBindingNeedsRebuild`。Coordinator 已有的安全重建路径会从 Logic 加载已应用
Edit/Recall 语义的权威有效历史，用 nil Session 启动 Pi，并以旧 generation 为 base
提交新 generation。它不解析旧 JSONL、不复制废弃分支、不重放 Tool。

本轮已经 settled 且未超过硬上限的 Candidate 仍可提交；在下一轮边界 rollover。
这样不会因为刚跨过软阈值而丢失已经冻结的最终消息。超过硬上限的 Candidate 仍立即
拒绝，不能用扩容掩盖失控会话。

## 回归矩阵

- Candidate 记录精确 byte size 和 entry count；
- 最后一条 JSONL frame 没有 LF 时仍计为一个 entry；
- 字节恰好达到软阈值时下一轮要求 rebuild；
- entry 数恰好达到软阈值时下一轮要求 rebuild；
- rollover 同时可被通用 rebuild 分支与运维分类识别；
- Candidate 超过 `max_snapshot_bytes` 继续 fail closed；
- 配置拒绝软字节阈值大于硬上限、非正 entry 阈值；
- 小型自定义硬上限会得到安全的折半默认软阈值。

## 可重现命令

```bash
GOCACHE=/tmp/resonance-gocache \
  go test ./pilot/session ./pilot/config ./pilot/coordinator ./pilot -count=1

GOCACHE=/tmp/resonance-gocache \
  go test -race ./pilot/session ./pilot/config ./pilot/coordinator -count=10

GOCACHE=/tmp/resonance-gocache go test ./... -run '^$'
GOCACHE=/tmp/resonance-gocache go vet ./...
```

本轮 Session、Config、Coordinator 功能测试、10 轮 Race 以及全仓编译/Vet 已通过。

## 运维解释

`candidate_session_bytes/candidate_entry_count` 和 `session_bytes/entry_count` 可用于
容量趋势与告警，但授权与业务状态仍只能来自 Go/IAM/PostgreSQL 权威事实。旧对象由
已有的 cluster-wide GC 在不再被 Binding 或非终态 Run 引用、且超过 grace 后回收。
多主机部署仍必须使用已验证的共享 CAS Store；本地 named volume 的 rollover 不会把
它变成 HA 存储。
