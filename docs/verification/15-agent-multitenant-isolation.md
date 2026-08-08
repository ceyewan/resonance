# Agent 多租户并发隔离验证

日期：2026-08-09

## 目标与边界

本门禁验证多个租户工作负载并发运行时，Session、Capability、Tool Result 和可观测面不会跨租户串扰。生产拓扑中，每个 Pilot 实例固定绑定一个 Tenant 和 Profile；测试因此建立 8 个独立配置的工作负载，而不是在一个 Pilot 进程内动态切换 Tenant。

这是确定性隔离与容量门禁，不调用真实 Provider，也不替代候选镜像的 Provider Observation 和发布环境回滚实操。

## 拓扑与并发量

- 8 个 Tenant，每个 Tenant 独立 Capability signing secret、Broker UDS 和 Session root；
- 每个 Tenant 同时发起 8 个 Run，单轮共 64 个并发 Run；
- Race Detector 连续执行 20 轮，共完成 1,280 个 Run；
- 每个 Run 使用唯一的 Tenant、Actor、Conversation、Session 内容和 Capability。

## 必须成立的隔离不变量

1. 己签发的 Capability 只能在所属工作负载的 Broker 使用，重放到相邻 Tenant Broker 必须返回 401。
2. `get_my_profile` 只返回当前 Run 的脱敏身份，不得出现其他 Tenant 的 username、password marker、Session 字节或原始 Capability。
3. Session staging 目录全局唯一，candidate 从所属 Session root 读回后必须与本 Run 的预期字节和大小完全一致。
4. 即使调用者替换另一 Tenant 的全部不透明 Session 绑定字段，当前 Tenant 的 Session Manager 也必须拒绝恢复。
5. Secret 通过 `String`/`fmt` 进入可观测面时只能显示脱敏值；Pilot 配置 JSON 不得包含 PostgreSQL、NATS、Etcd、Capability 或 service-auth Secret 原值。
6. Runtime 指标标签不得包含 tenant、user、conversation、run、call 或动态 Tool 名称；这些身份值不进入 metrics cardinality 或 label value。

## 可重现命令

```bash
GOCACHE=/tmp/resonance-gocache go test ./pilot/isolation ./pilot/config ./pilot/observability -count=1
GOCACHE=/tmp/resonance-gocache go test -race ./pilot/isolation -run '^TestConcurrentTenantIsolation$' -count=20
GOCACHE=/tmp/resonance-gocache go vet ./pilot/isolation ./pilot/config ./pilot/observability
```

CI 在每次变更上固定执行 5 轮 Race Detector 门禁。在本地受限沙箱中，HTTP-over-UDS 可能需要允许 Unix socket 监听；该限制不应通过改用 TCP 或放宽 Broker 地址来绕过。

## 验证结果

- 64 个并发 Run 的功能门禁通过；
- 20 轮 Race Detector 门禁通过，共 1,280 个 Run；
- 跨 Tenant Capability 重放和 Session 绑定替换均被拒绝；
- 配置 Secret 脱敏和无身份指标标签的回归测试通过。

## 保留门禁

本测试共享一台主机的 CPU 和文件系统并发度，但不是生产集群的长时间 soak test。候选发布仍必须在独立测试 Tenant 执行真实 Provider Eval，并实操 control/runtime digest 组合回滚与旧 Session 恢复。
