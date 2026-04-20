# Web 文档索引

`web/docs/` 只放 **`web/` 目录内部的开发文档**，重点服务两类协作角色：

- `UI/UX` 页面开发
- 前端运行时 / 数据流 / 业务逻辑开发

## 文档分工

| 文档 | 面向对象 | 用途 |
|------|----------|------|
| [UI-HANDOFF.md](./UI-HANDOFF.md) | UI/UX 页面开发 | 约束页面层应该依赖什么，不该碰什么 |
| [RUNTIME-HANDOFF.md](./RUNTIME-HANDOFF.md) | 前端业务逻辑开发 | 说明 runtime、service、sync、db、ws 的职责边界与扩展方式 |

## 建议的协作边界

- `UI/UX`
  - 负责页面、布局、组件组合、视觉样式、交互细节
  - 通过 `hooks / services` 消费稳定接口
- `Runtime / Logic`
  - 负责 ConnectRPC、WebSocket、Dexie、Outbox、Inbox Sync、状态机与数据一致性
  - 对 UI 层暴露稳定的 `hook / service` 契约

如果页面开发过程中需要直接 import `Dexie`、`WsClient`、`authClient`、`sessionClient`，通常说明接口层还没收好，应该先补 `hook / service`，而不是让页面层下钻。
