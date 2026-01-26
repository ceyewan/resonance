# internal 说明与待办

`internal` 是 Resonance 的业务 DAL/SDK 层：提供模型与仓储接口，供 Logic/Task 等服务调用。当前以 DB/Redis 直连为主，优先保证业务可用与清晰边界。

## 现状与已覆盖能力

- 用户、会话、消息、信箱的基本 CRUD 能力已具备。
- MessageRepo 支持 Outbox Pattern（事务内写消息 + 记录 outbox）。
- RouterRepo 提供用户与网关映射（Redis）。

## 仍需补齐的能力（从 AUDIT 整合）

1. **缓存层（Decorator）**
   - 目标：User/Session 读多写少场景加速鉴权与会话查询。
   - 策略：读穿透（Read-Through）+ 写后失效（Cache Aside）。
   - 实现建议：`CachedUserRepo`/`CachedSessionRepo` 包装现有 Repo，对上层透明。

2. **消息批量落库**
   - 目标：Logic 侧批量写入消息内容，提升吞吐与减少 DB 压力。
   - 缺口：`MessageRepo` 尚未提供 `BatchSaveMessages(ctx, msgs)`。

3. **消息漫游缓存**
   - 目标：历史消息查询的低延迟。
   - 方案：Redis ZSET（key: `msg:{session_id}`，score: `seq_id`，value: message json）。
   - 写入：Task 消费后先写 Redis，再异步/批量落库。
   - 读取：先 Redis，不足再回源 DB。

4. **SeqID 生成能力下沉**
   - 目标：将 `IncrSeqID` 逻辑封装进 `internal`，统一序列号生成策略。
   - 原则：业务层仅调用接口，避免暴露基础组件。

## 设计原则

- 业务优先：先满足 IM 业务场景，再扩展基础能力。
- 依赖透明：`internal` 对上层提供稳定接口，基础组件细节在内部消化。
- 可演进：按需引入缓存/批量/幂等等能力，不做过度设计。
