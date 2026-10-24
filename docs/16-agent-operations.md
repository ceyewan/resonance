# Pilot 部署与维护

## 发布形态

只有一个 Pilot 镜像 `pilot-final`，包含 Go 控制逻辑、Node 与固定 Pi；不再部署 Runtime sidecar、IAM Pilot、Tool Broker 或出口代理。
Compose 运行非 root、只读 rootfs、无宿主目录/Socket，仅 Session 数据卷和有界临时目录可写。

Genesis 使用正式版 `v1.0.0`，Go 工具链为 `1.26.10`（Genesis 要求至少 `1.26.9`）。

## 配置

- `.env` 配置 `DASHSCOPE_API_KEY/BASE_URL/MODEL`。
- `RESONANCE_PILOT_SERVICE_AUTH_SECRET` 与 Logic 的 Pilot 密钥一致，与 JWT/Gateway 密钥不同。
- 助手 Prompt、输入/输出上限、并发、超时与 Session 目录在 `configs/pilot.yaml`。
- Provider 凭证仅注入 Pilot；Pi 子进程只继承明确白名单，不接收数据库和服务签名凭证。
- 生产 Pilot 镜像固定 digest，关闭自动升级。回滚时恢复此前 Pilot digest 并保留数据卷。

本地更新：`make update-local`。使用 `--remove-orphans` 清理已删除的旧服务，不使用 `down -v`。

## 数据初始化

本轮仅支持全新数据库，直接运行 init 创建当前模型、默认群和 Bot；用户注册时创建身份并补齐唯一 Pilot 私聊。
不提供旧审批、多 Profile API、历史数据回填或旧库迁移脚本。
不需要为了部署本次代码执行任何兼容迁移；已有数据环境不属于本轮支持范围。

## 最小检查

```bash
go build ./...
go test ./pilot/... ./logic/service ./logic/server ./logic/config ./gateway/logicclient ./pkg/iam
go test ./repo -run TestPilotConversationIsUniquePerTenantAndOwner
npm --prefix pilot/bridge run typecheck
npm --prefix web run type-check
```

真实 Provider smoke 需显式 `RESONANCE_LIVE_AGENT_E2E=1`，会创建本地测试用户并消耗额度，不应默认执行。

## 故障语义

- Provider 失败：Run 有界重试并进入终态，不能无限循环。
- Pilot 重启：过期租约恢复；已冻结结果不重新推理。
- Web 断线：不取消生成，通过 Inbox/History 恢复最终消息；不重放临时 delta。
- Session 损坏或 Runtime 格式变化：从有效历史重建，不手改 Pi JSONL。
- 正常退出：停止摄入和 claim，等待 Active Run，超时 Abort，回收进程后再关闭依赖。

当前以单实例 Pilot + 本地持久卷为部署基线，不宣称跨主机 Session 高可用。
