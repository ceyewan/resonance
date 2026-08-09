# Agent Service Operations

本文定义 Pilot + Pi Runtime 的发布、Canary、回滚和故障恢复流程。最终用户可见事实仍由 Logic 的 Message/Outbox/Inbox 链路提交；`AgentStreamEvent` 只是可丢失的临时展示。

## 发布单元与不变量

Pilot control 镜像与 Runtime sidecar 镜像是一个兼容发布单元，必须一起固定和记录：

- Go Pilot/control 二进制及 Remote Runtime 协议版本；
- Runtime 镜像中的 Node major、Pi 精确版本和 lockfile；
- 可信 TypeScript Bridge；
- Runtime RPC 契约和 Session 兼容策略；
- Tool Manifest、Profile ID 与 Profile Version。

生产环境必须分别使用不可变的 control/runtime 镜像 digest，并记录两者的兼容组合。两个 Pilot control 和两个 profile-specific Runtime 都被明确排除在 Watchtower 自动升级之外，不得使用浮动 `latest` 作为实际部署版本。

以下条件任一不满足时禁止开始接收新 Run：

1. `/ready` 返回成功；
2. PostgreSQL、NATS、Etcd 和 Logic 可用；
3. Runtime sidecar 就绪，Pi `--version` 与 `runtime.expected_version` 精确相等；
4. Remote Runtime 与 Tool Broker 只监听 profile-specific 私有 UDS，Capability secret 已配置；
5. Session root 可写、权限为 `0700`，Runtime 和 Bridge 文件为可信普通文件；
6. Provider secret 只存在于 Runtime sidecar，control/proxy 均不可见，且没有被写入配置 dump、日志或 Pi stdout；
7. 当前 Session fixture 已通过目标 Runtime 的恢复契约测试。
8. user-assistant 与 iam-admin 的 service ID、service-auth secret、Capability secret、Runtime UDS 和 Session volume 均不同，Logic 方法/Tenant allowlist 与 Profile version 已同步；
9. Tenant 存在显式启用的 Budget Policy，日/月 Token、micro-USD 与单 Attempt 上限已经过复核；
10. 候选镜像的真实 Provider 业务 Eval 报告通过，并绑定当前镜像 digest、Pi/Bridge/Profile version。

## CI 发布门禁

在构建不可变镜像前运行：

```bash
(cd api && buf lint)
go test ./pilot/... -count=1
go test -race ./pilot/runtime/... ./pilot/coordinator/... ./pilot/toolbroker/... -count=5
go test -race ./pilot/isolation -run '^TestConcurrentTenantIsolation$' -count=5
go vet ./pilot/...
npm --prefix pilot/bridge run typecheck
npm --prefix pilot/bridge test
go test ./pilot/eval -count=1
```

使用镜像内固定的 Pi 执行离线 RPC 契约；该测试不得访问模型 Provider：

```bash
RESONANCE_PI_BINARY="$PWD/pilot/bridge/node_modules/.bin/pi" \
RESONANCE_PI_EXPECTED_VERSION=0.84.1 \
RESONANCE_PI_BRIDGE="$PWD/pilot/bridge/src/index.ts" \
go test -tags=pi_contract ./pilot/runtime/pi -run TestRealPiRPCContract -count=1
```

带真实 Provider 的 Session resume/compaction fixture、Tool Eval 和副作用 Eval 必须使用独立的受控 CI 环境与最小权限测试租户。它们不能被 Fake Process 测试替代。

镜像门禁还必须构建 `pilot-control-final` 与 `pilot-runtime-final`，证明 control 内没有 Node/Pi、两者均为非 root 用户，Runtime 内 Pi 精确为 0.84.1。私有 UDS、凭证和网络分离见 `verification/13-agent-runtime-isolation.md`；业务 Observation 评分见 `verification/14-agent-business-eval.md`；多租户并发串扰门禁见 `verification/15-agent-multitenant-isolation.md`。

## Canary 流程

1. 记录当前 control/runtime 两个镜像 digest、Remote 协议、Pi/Bridge/Profile 版本和回滚 Session fixture。
2. 先部署一个独立 Canary 实例，使用独立的 NATS queue group、Worker ID 和测试租户；不能与生产 queue group 竞争消息。
3. 对 Canary 运行普通问答、只读 Tool、断线恢复、Abort、Pi crash、429/5xx、Session resume/compaction 和最终消息幂等检查。
4. 确认 Delta 丢失只影响临时气泡，最终 ChatEvent 可从 Inbox 恢复。
5. 将少量允许的租户显式加入 Canary admission allowlist；观察至少一个完整业务窗口。
6. 只有在错误率、首 token、Run duration、Tool 拒绝、Session CAS、成本和资源指标均符合基线后，才逐步扩大流量。

Canary 不得通过同时订阅生产 topic 的相同 queue group 实现；这会随机偷走生产事件。正式多实例扩容必须共享同一套 durable Run 数据库，并依赖会话 Active Run 唯一约束和租约恢复保证顺序。

## 停止摄入与排空

正常退出顺序固定为：

1. `/ready` 立即变为失败；
2. 取消 Agent ingress 订阅，停止新 claim；
3. 保持数据库、NATS、Logic、Tool Broker 和 lease heartbeat 可用，等待 Active Run 到达可恢复终态；
4. 超过 `shutdown_drain_timeout` 后执行 Pi RPC Abort；
5. 依次升级为进程组 TERM、KILL，并且每个子进程只 `Wait` 一次；
6. 最后关闭 Broker、Session Store、外部连接、metrics/trace 和 health。

不得先关闭数据库或 Tool Broker 再排空 Run；不得在关闭期间让 `/ready` 继续返回成功。

## 回滚原则

回滚对象是已验证的 control/runtime 镜像组合，不是单独替换 Pi npm 包。部署系统必须同时保留 `PILOT_PREVIOUS_IMAGE_DIGEST` 与 `PILOT_RUNTIME_PREVIOUS_IMAGE_DIGEST`，回滚时执行等价操作：

```bash
RESONANCE_PILOT_IMAGE="$PILOT_PREVIOUS_IMAGE_DIGEST" \
RESONANCE_PILOT_RUNTIME_IMAGE="$PILOT_RUNTIME_PREVIOUS_IMAGE_DIGEST" \
docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml \
  up -d --no-deps pilot-runtime pilot-iam-admin-runtime pilot pilot-iam-admin
```

执行前必须确认两个变量都是已记录的 `repository@sha256:...`，不能使用空值或浮动 tag。先等待两个 Runtime 的 `/ready` 与 Pi 版本探针，再允许两个 Pilot control 恢复 ingress。

仓库中的 `deploy/scripts/rollback-agent.sh` 将上述顺序固化为默认只校验、显式 `--execute` 才变更环境的操作。它会拒绝 tag/空值/非法 digest，先停止两个 control，再恢复并核对两个 Runtime，最后恢复并核对两个 control。脚本生成不覆盖的 `0600` JSON 证据，但它只能证明 digest 和 readiness；旧 Session fixture 的恢复结果必须由候选环境演练另行记录，并在恢复更广的 Tenant admission 前通过。

如果新版本已经提交了旧 Runtime 无法读取的 Session snapshot：

- 不回写或手工修改 Pi JSONL；
- 将对应 `t_agent_session_binding` 标记为 `DIRTY`；
- 下一轮从 Logic 权威 ChatEvent 历史重建新 Session；
- Tool 调用不得从历史重放。

## 故障处置

| 现象 | 立即动作 | 数据恢复原则 |
|---|---|---|
| Pi 版本探针失败 | 保持 not-ready，停止发布 | 修复镜像或回滚，不放宽版本匹配 |
| stdout 协议污染/超限 | Abort 并回收进程，记录脱敏分类 | Run 按可重试策略收敛，不解析普通文本 |
| Provider 429/5xx | 受限退避，观察租户预算 | 不产生第二条最终消息 |
| Budget Policy 缺失/禁用/耗尽 | 保持 Run 未启动，检查 Policy/Bucket | 不绕过门禁，不手工释放 UNKNOWN hold |
| Runtime UDS 不可达 | Runtime/control 均 not-ready，按镜像对重启 | 请求 outcome unknown 时保留 Attempt reservation |
| `READY_TO_COMMIT` 堆积 | 保持 Runtime 停止重推理，恢复 Logic/DB | 只重试冻结结果的最终提交 |
| 最终消息已写但 ACK 丢失 | 用固定 client_msg_id 重试 | Logic 返回首次 ACK，不再写 Outbox |
| Session checksum/CAS 失败 | 隔离对象并标记 binding dirty | 从权威 ChatEvent 重建，禁止猜测合并 |
| Tool Broker 授权失败 | fail closed，撤销该 Capability | 不允许 Pi 或 Prompt 覆盖授权结果 |
| 审批过期/参数哈希不符 | 拒绝执行并写审计 | 不重新生成或替换冻结参数 |
| 流式通道拥塞 | 丢弃有界 Delta，必要时发 Error End | 最终 Inbox 消息保持权威 |

## Session 对象维护

不可变 Session 对象只有在以下条件同时满足时才能回收：

- 不被任何 ACTIVE/DIRTY binding 引用；
- 不被 QUEUED、Active 或 `READY_TO_COMMIT` Run 的 candidate ref 引用；
- 创建时间早于配置的 GC grace period；
- 回收任务持有集群级单例锁，并在删除前再次读取引用集合。

当前实现通过 PostgreSQL transaction advisory lock 串行化同一 Session root 的 GC，并在锁内读取所有 Tenant 的 binding/candidate reference。默认间隔为 30 分钟、grace 为 24 小时；损坏对象或未知路径会被保留并报告。

Pi compaction 不会缩小 Session JSONL。默认 `rollover_bytes=32 MiB`、`rollover_entry_count=20000`，硬上限 `max_snapshot_bytes=64 MiB`。达到任一软阈值后，下一轮必须从 Logic 权威有效历史建立新 generation；不得临时调高硬上限来掩盖持续膨胀。`t_agent_run.candidate_session_bytes/candidate_entry_count` 和 `t_agent_session_binding.session_bytes/entry_count` 是容量查询依据，实际恢复时仍以对象 checksum 和真实文件计数为准。

禁止用“当前目录里看不到 staging”作为删除依据。共享存储的多实例部署必须使用支持原子 publish、校验和读取和对象生命周期策略的 Session Store；单机 named volume 不能冒充多可用区存储。

## Budget Policy 上线门禁

Pilot 默认且只支持 `budget.policy_mode=require_explicit`。新 Tenant 没有 Policy 时不会启动 Pi。当前 Policy 通过受控数据库变更流程初始化；发布前至少复核以下不变量：

- `monthly >= daily >= max_attempt > 0`，Token 与 micro-USD 分别成立；
- 单 Attempt Token/Cost 不超过 `9_007_199_254_740_991`；
- 金额单位为 micro-USD，不是美元浮点；
- Policy 更新使用当前 `version` 做 CAS，不能原地覆盖并发变更；
- UNKNOWN Attempt 继续占用 reservation，只有人工完成 Provider 对账后才允许走单独的恢复流程。

建议先以 `enabled=false` 写入并复核，再使用 Repo 的 `PutAgentBudgetPolicy(expectedVersion)` 启用；不得增加“Policy 缺失即无限额”的兼容开关。Policy、Bucket 和 Attempt 的只读核对 SQL 见 `verification/11-agent-budget-ledger.md`。

## Egress 门禁

应用内关闭 Shell、文件、任意 HTTP Tool 不能替代网络层 Egress 控制。当前 Compose 将 Pi/Node/Bridge 放在只连接 `runtime-internal` 的 Runtime sidecar；它不能直达业务网络，只能经双网 CONNECT proxy 到达精确允许的 Provider endpoint。生产发布必须保持：

- 配置的模型 Provider endpoint；
- Runtime 只允许 Provider proxy 和本机 Tool Relay；
- control 才允许 PostgreSQL、NATS、Etcd、Logic 和可观测性后端；
- proxy 自己解析 DNS、拒绝私网/metadata/mixed answers，并按 CONNECT host 校验 TLS SNI/ALPN。

任何把 Runtime 重新加入 `resonance-net`、直接赋予公网默认路由、把 Provider Key 注入 control，或放宽 proxy 为任意 host/IP/端口的变更，都必须作为安全 breaking change 重新评审。完整故障矩阵见 `verification/12-provider-egress-proxy.md`。
