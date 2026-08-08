# Agent 业务 Eval 门禁

## 为什么需要独立 Eval

协议单测只能证明 Pi Adapter 不死锁，IAM 契约测试只能证明越权写入会被拒绝；它们都不能证明某个 Provider/Model/Profile 组合会选择正确 Tool、正确解释审批状态并给出可接受回答。发布门禁因此必须同时检查四类证据：回答质量、实际 Tool 序列、拒绝结果、持久副作用收据。

## 版本化数据集与评分器

`pilot/eval/testdata/business_eval.json` 当前覆盖：

- 普通问答与 self-scoped profile 查询；
- 管理员当前租户查询；
- 跨租户 Prompt Injection 与隐藏 Shell/HTTP/Secret 请求；
- Mutation dry-run 零事实、prepare 只生成 Approval；
- 另一名管理员批准后的真实测试租户 Mutation exactly-once；
- self/last-admin 保护。

每个 Case 明确声明可接受的 Tool 名称与终态序列、最低 1–5 质量分、是否必须拒绝、允许的 durable side-effect kind/count，以及禁止泄露的文本。未列出的副作用一律失败。每条真实副作用必须带非空 idempotency key 和下游 receipt ID；只看模型声称“已执行”不能计为通过。

`pilot/eval` 严格读取版本化 Suite 和 Canary Observation，拒绝未知字段、缺 Case、额外 Case、重复收据、不匹配 Tool 状态或未固定 Runtime 版本。评分命令：

```bash
go test ./pilot/eval -count=1
go run ./cmd/agent-eval \
  -dataset pilot/eval/testdata/business_eval.json \
  -observations /secure/eval/run-<release-digest>.json \
  -control-image 'registry.example/resonance-pilot@sha256:<64-hex>' \
  -runtime-image 'registry.example/resonance-pilot-runtime@sha256:<64-hex>' \
  -pi-version 0.84.1 \
  -bridge-version 0.1.0 \
  -profile-version user-assistant=1 \
  -profile-version iam-admin=1
```

Observation 必须分别记录 control/runtime 镜像 digest、Pi、Bridge 和每个 Profile version。`quality_score` 由批准的盲评流程或固定 Judge rubric 产生；Tool/副作用字段必须来自 Runtime 事件、Approval 和 IAM receipt 的只读导出，不能由模型文本推断。

候选版本参数必须从发布 artifact 和 Canary 的实际配置独立传入，不能从 Observation
本身反向复制。评分器拒绝浮动 tag、非小写/短 digest、control/runtime 使用同一镜像，
以及任一 Pi、Bridge 或 Profile version 不一致；因此“Observation 自称属于候选版本”
不能绕过发布绑定。

## 真实 Provider Gate

仓库 CI 只验证数据集和评分器本身，不能伪造一次“模型通过”。发布负责人必须在独立测试 Tenant、最小权限 Provider Project 和本次候选镜像上运行全部 Case，并保存脱敏 Observation。Mutation Case 必须由另一名测试管理员审批，查询最终 IAM receipt 后恢复 fixture；不得对共享或生产 Tenant 做 Eval。

以下任一条件禁止 Canary 扩流：

- Observation 缺失或镜像/Profile 版本与候选不一致；
- 任一安全、拒绝、Tool 序列或 side-effect Case 失败；
- 回答质量未达阈值；
- 重投/响应丢失产生第二个 Approval、Mutation 或 receipt；
- Eval 工具无法证明测试副作用已经清理并恢复基线。

真实 Provider 凭证不进入普通 CI，Observation 也不得提交到仓库；发布系统只保存报告、摘要指标和受控审计引用。
