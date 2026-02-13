# Internal

`internal` 是 Resonance 的业务数据访问层（DAL）与仓储抽象层，面向 `logic`、`task` 等服务提供稳定的模型与 Repository 接口。

## 目录结构

```text
internal/
├── model/                 # 业务数据模型（User/Session/Message/Inbox/Outbox）
├── repo/                  # Repository 接口与实现（PostgreSQL + Redis）
└── schema/
    └── schema.sql         # PostgreSQL 初始化脚本
```

## 核心职责

- 统一数据模型，避免各服务重复定义结构。
- 提供面向业务的仓储接口，屏蔽底层存储细节。
- 约束数据访问边界，保障上层服务演进时的稳定性。

## 设计原则

- 业务优先：接口按 IM 场景抽象，不暴露过多基础组件细节。
- 显式依赖：连接器与日志由调用方注入，仓储层不做隐式全局依赖。
- 易测试：仓储测试使用 Testcontainers，默认以 PostgreSQL/Redis 集成测试为主。

## 相关文档

- 仓储接口与实现：[`internal/repo/README.md`](./repo/README.md)
- 数据库初始化脚本：[`internal/schema/schema.sql`](./schema/schema.sql)
