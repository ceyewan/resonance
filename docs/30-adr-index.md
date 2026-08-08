# 架构决策记录索引

> 本文档索引已经正式确认、需要长期追溯的架构决策。设计细节仍放在对应主题文档，ADR 只记录背景、选择、替代方案和后果。

| ADR | 状态 | 日期 | 决策 |
| --- | ---- | ---- | ---- |
| `ADR-001-pilot-pi-runtime.md` | Accepted | 2026-08-08 | Pilot 采用 Go 控制面 + Pi Harness Runtime + Go Tool Broker |

## 状态说明

- `Proposed`：仍在评审，不能作为实现依据。
- `Accepted`：当前实现应遵循。
- `Superseded`：已由新 ADR 替代，保留用于追溯。
- `Rejected`：方案未采用，保留拒绝原因。

## 维护规则

- 改变 Runtime 主选型、Session 一致性协议或 Tool 授权边界时，必须新增 ADR，不直接覆盖旧决策。
- 纯实现细节、参数调优和不改变边界的重构不需要 ADR。
- ADR 与主题文档冲突时，以最新 Accepted ADR 为决策依据，并立即修正文档冲突。
