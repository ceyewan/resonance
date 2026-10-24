# 部署

- 本地：`make up`；更新：`make update-local`。
- 生产：`make up-prod`，先配置 `.env` 中的独立签名密钥、Provider 与 Pilot digest。
- 基础设施：`make up-infra`。
- 可观测性：`make up-observability`。

应用镜像 target 为 `final`；Pilot 为 `pilot-final`（Go + 固定 Pi）。不存在 IAM 助手或独立 Runtime 服务。
`--remove-orphans` 会移除旧 sidecar，不会删除数据卷。

本轮仅支持全新数据库，直接运行 init 建表并创建种子数据；不提供历史数据回填或旧库迁移。

详细配置见 [部署文档](../docs/06-deployment.md) 与 [Pilot 运维](../docs/16-agent-operations.md)。
