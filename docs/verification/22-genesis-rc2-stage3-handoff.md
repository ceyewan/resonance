# Genesis RC2 Stage 3 证据交接契约

## 状态与目的

本文只定义 Genesis `v1.0.0-rc.2` 发布前的 Stage 3 证据 schema、生成流程和固定发布路径。当前仓库没有正式 handoff manifest，也没有把任何尚未运行的检查写成 `PASS`。只有六类 Stage 3 检查在同一个不可变输入集上真实通过后，才允许由 evidence-only protected PR 新增正式 JSON。

固定路径是：

```text
docs/verification/evidence/genesis-v1.0.0-rc.2-stage3.json
```

这个路径不能由调用者覆盖。文件不存在、位于其他路径、是符号链接，或者大于 1 MiB 时，Genesis 发布门禁都必须失败。

## 严格 JSON schema

正式文件是单一 JSON object。Genesis 验证器使用 `DisallowUnknownFields`，并拒绝重复 key、尾随 JSON 值、缺失字段和非规范值。下表是完整字段集合；文档没有提供可被误认为已通过证据的示例 manifest。

| JSON path | 类型 | 必须满足的条件 |
| --- | --- | --- |
| `schema_version` | string | 精确等于 `genesis-stage3-evidence/v1` |
| `status` | string | 六项检查真实通过后精确等于 `PASS` |
| `tested_at` | string | 非零、规范 UTC RFC3339 时间，必须以 `Z` 结尾，不能比验证时钟晚超过五分钟 |
| `tested_resonance_sha` | string | 非零、完整、小写 40 位 Git commit SHA |
| `genesis_rc1.version` | string | 精确等于 `v1.0.0-rc.1` |
| `genesis_rc1.sum` | string | 精确等于 `h1:X3VK5VpPxIrgyzQsPPPSHQHaiNvMhhT/wcGCWkuFS8U=` |
| `genesis_rc1.go_mod_sum` | string | 精确等于 `h1:VUPsG33Toz8lKJk2tEkgeWd7SFMIDjYtwvzYOuQmRU4=` |
| `inputs.compose_sha256` | string | 非零、小写 `sha256:<64hex>`，对应实际展开并执行的 Compose 输入 |
| `inputs.application_image` | string | application 的不可变 `name@sha256:<64hex>` |
| `inputs.pilot_control_image` | string | pilot control 的不可变 `name@sha256:<64hex>` |
| `inputs.pilot_runtime_image` | string | pilot runtime 的不可变 `name@sha256:<64hex>` |
| `checks.<name>.status` | string | 精确等于 `PASS` |
| `checks.<name>.completed_at` | string | 非零、规范 UTC RFC3339 时间，不得晚于顶层 `tested_at` |
| `checks.<name>.evidence_sha256` | string | 对应不可变原始证据记录的非零、小写 `sha256:<64hex>` |

`checks` 必须恰好包含以下六个 object：

- `compose`：完整 Compose 健康检查与服务发现；
- `im`：send、edit、recall、read 与 offline E2E；
- `agent`：Pilot run、stream、tool/approval、commit/status E2E；
- `recovery`：重启、依赖故障与恢复路径；
- `telemetry`：metrics、logs、traces、correlation 与 alerting；
- `benchmark`：代表性并发 benchmark 与容量记录。

每一个 check object 都必须同时包含 `status`、`completed_at` 和 `evidence_sha256`。SHA-256 应绑定实际保存的原始输出或证据 bundle，不能填写预期值，也不能从另一个输入 manifest 的结果复制。

Genesis 验证器不会根据 `evidence_sha256` 取回或重新散列 Stage 3 原始 bundle；它只验证 digest 的格式和非零性，并把这些 protected-PR attestation 绑定到最终 manifest digest。因此，原始 bundle 的耐久、不可变存档以及可审计 locator 是创建 evidence-only PR 之前的独立前置条件。locator 必须进入 Stage 3 执行交接记录，并在 Genesis `release` environment 的人工审批记录中被引用和确认仍可读取；当前四字段输出、Genesis consumer/release-evidence artifact 和 tag annotation 都不携带 locator。单独一个手填 digest 不证明检查确实执行，也不构成完整机器证据。

## 生成与 protected PR 流程

1. 在一个已合并、受保护的 Resonance commit 上冻结 Stage 3 输入：Resonance source SHA、展开后的 Compose digest，以及 application、pilot control、pilot runtime 三个镜像的 `name@sha256`。
2. 六类检查全部使用同一组冻结输入执行。任何输入变化都会产生新 manifest，已有结果不能沿用。
3. 把六份原始证据保存到耐久、不可变的位置，在 Stage 3 交接记录中登记可审计 locator，再计算各自 SHA-256；最后一个检查完成时间写入顶层 `tested_at`，被测试的 source commit 写入 `tested_resonance_sha`。
4. 从该 tested commit 新建 evidence-only PR。PR 必须只新增固定路径 JSON，不得同时修改代码、配置、文档或其他证据文件。
5. 通过正常 branch protection 和 required checks 合并。合并后的 commit 是 Genesis 发布流程使用的 `consumer SHA`；它与 `tested_resonance_sha` 可以不同，但前者只能多出这一份 JSON。
6. Genesis 在该 consumer SHA 的精确、干净 checkout 上重新读取文件、验证 Git 祖先关系与 diff、校验 committed `go.mod`/`go.sum` 的 RC1 identity，并从文件原始 bytes 计算 handoff SHA-256。

如果 evidence-only PR 需要修正内容，应关闭或替换它并重新生成证据；不要让验证器接受手工提供的摘要，也不要在原 tested commit 上预埋 placeholder manifest。

## Genesis 发布门禁调用约定

Genesis checkout 中的调用命令是：

```bash
go run ./internal/cmd/stage3evidence \
  --repo "$RESONANCE_CHECKOUT" \
  --consumer-sha "$RESONANCE_CONSUMER_SHA"
```

`--repo` 必须指向 Resonance repository root，checkout 必须干净且 `HEAD` 精确等于完整 `--consumer-sha`。manifest 中的 tested SHA 必须是 consumer SHA 的祖先；`git diff --name-status --no-renames <tested> <consumer>` 必须精确得到一条固定 JSON 的新增记录。

验证成功时，标准输出只有以下四个 `key=value`，可直接追加到 GitHub Actions 的 `$GITHUB_OUTPUT`：

```text
stage3_manifest_path=docs/verification/evidence/genesis-v1.0.0-rc.2-stage3.json
stage3_manifest_sha256=<lowercase-64hex>
stage3_tested_resonance_sha=<full-lowercase-40hex>
stage3_tested_at=<canonical-UTC-RFC3339>
```

任一校验失败时命令向标准错误输出原因并以非零状态退出，不产生可用于发布的 output。Genesis 后续的 consumer artifact、release-evidence bundle 和 tag annotation 必须使用这次命令派生的四个值，不能接受手工填写的 manifest digest，也不能声称这些字段包含原始 bundle locator。发布负责人仍须在独立的 release-environment 审批记录中核验 Stage 3 交接记录所列存档位置、可用性和内容；这个 lineage 和 digest 绑定门禁不替代该审计。
