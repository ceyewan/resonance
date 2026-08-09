# Genesis v1 RC 集成验证

## 结论

Resonance 从 Agent Service 合并后的 `origin/main` 精确 SHA
`0ea1a2fd549ac4900c42f93ff0eb67ea3a5f4e56` 派生，固定使用公开发布的
`github.com/ceyewan/genesis@v1.0.0-rc.1`。本次适配不包含 `replace`、`go.work`
或 Genesis 源码副本。

## Published module 证据

`GOWORK=off go list -m -json github.com/ceyewan/genesis` 返回：

| 字段 | 值 |
| --- | --- |
| Version | `v1.0.0-rc.1` |
| Tag SHA | `ec5ad2c31fb4adce2bd42529e3d7fbfe92b23aa7` |
| GoVersion | `1.26` |
| Sum | `h1:X3VK5VpPxIrgyzQsPPPSHQHaiNvMhhT/wcGCWkuFS8U=` |
| GoModSum | `h1:VUPsG33Toz8lKJk2tEkgeWd7SFMIDjYtwvzYOuQmRU4=` |

## 适配范围

- Gateway、Logic、Task、Pilot、Bootstrap 与 Webserver 对齐 Genesis v1 的
  constructor、错误返回和关闭契约。
- 服务初始化失败按逆序回滚；正常关闭使用有界 context、聚合关键错误，并通过
  `sync.Once` 保证重复关闭安全。
- NATS/JetStream 显式配置 stream、AckWait、MaxDeliver、retention、storage、
  durable 与 consumer 起点；MQ 与 subscription 采用 Drain 语义。
- Gateway 生产 Compose 使用 Redis distributed limiter；standalone 仅作为开发默认值。
- Snowflake allocator 使用 Redis 共享 worker 池，并显式设置服务隔离 key prefix。
- Gateway、Logic、Task、Pilot 统一复用 Genesis trace 初始化与传播 helper；Trace 与
  Metrics 均执行 Shutdown，保留 Resonance 领域指标。
- CI 强制 `GOWORK=off`、精确 module identity、可达漏洞扫描、生成代码一致性、Web、
  Bridge/Pi 合约，以及 application/control/runtime 三类镜像契约。

## 本地门禁

以下命令于 2026-08-09 通过：

- `GOWORK=off go mod verify`
- `make gen`，随后 `git diff --exit-code -- api/gen web/src/gen`
- `make format`、`make lint`、`make lint-security`、`make test`
- `go test -race ./pilot/isolation -run '^TestConcurrentTenantIsolation$' -count=5`
- Web：`npm ci`、type-check、lint、build
- Bridge：`npm ci`、typecheck、test
- 使用锁文件 Pi binary 的 `TestRealPiRPCContract`，版本 `0.84.1`
- `final`、`pilot-control-final`、`pilot-runtime-final` 三个 Docker target 构建
- 三类镜像均为非 root；control 不含 Node/Pi，runtime 固定 Pi `0.84.1`
- 回滚校验接受两个不同 immutable digest，并拒绝 mutable tag

`govulncheck` 报告可达漏洞为 0。镜像仅在本地构建，没有 push、Release 或部署。

## Hosted CI

PR #9 在正常配置提交 `7dcd3a8` 上 9 项检查全部通过。受控负向提交
`51f22b5` 临时将 Genesis module sums 设为错误期望；GitHub Actions run
`31293930017` 的 `Verify published Genesis module identity` 在 9 秒内失败，并跳过
后续 Go test 与 race gate，证明不匹配的 published module 不能通过合并门禁。随后恢复
真实 sums 并重新执行全量检查。

合并后 main SHA 与最终 main workflow run 在阶段 Goal 交接表中维护。
