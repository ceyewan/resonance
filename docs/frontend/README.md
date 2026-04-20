# 前端文档索引

`docs/frontend/` 用来承载 **Web 前端实现文档**，不再把页面、UI、运行时细节混放到 `docs/architecture/`。

## 文档边界

- `docs/architecture/`
  - 只保留系统级架构：协议、数据库、服务职责、时序、迁移计划、部署、测试
- `docs/frontend/`
  - 保留前端专项内容：Web 运行时、数据流、页面分层、UI 设计、视觉方案、交付计划
- `web/docs/`
  - 保留前端目录内部的开发接手说明、页面层约束、实现备忘

## 当前文档

| 文档 | 用途 |
|------|------|
| [01-web-architecture.md](./01-web-architecture.md) | 前端运行时架构、数据流、目录结构、实现阶段 |
| [02-liquid-glass-design.md](./02-liquid-glass-design.md) | 视觉概念、风格锚点、设计草图 |
| [03-native-liquid-glass-evolution.md](./03-native-liquid-glass-evolution.md) | 原生 Liquid Glass 组件能力演进路线 |
| [04-uiux-plan.md](./04-uiux-plan.md) | UI/UX 开发计划与组件落地建议 |
| [../../web/README.md](../../web/README.md) | 当前前端实现总览、运行命令、目录说明 |
| [../../web/docs/UI-HANDOFF.md](../../web/docs/UI-HANDOFF.md) | 页面层接手说明，指导 feature 直接消费 hook / service |

## 建议继续补充的文档

这几类文档目前还值得补，但不必放进 `docs/architecture/`：

1. `05-testing.md`
   - 前端单测、Vitest 用例边界、Dexie/WS mock 策略
2. `06-release-checklist.md`
   - 发版前检查项：构建、路由、runtime-config、接口兼容、离线恢复
3. `07-troubleshooting.md`
   - 常见问题排查：WS 连不上、ACK 超时、Dexie 数据异常、runtime-config 未注入

后续如果继续补，优先顺序建议是：`testing` → `troubleshooting` → `release-checklist`。
