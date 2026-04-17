# 07 - 部署收敛设计

> 前置阅读:`00-overview.md`(整体架构)、`03-services.md`(服务职责)、`06-layout-refactor.md`(目录重组)。本文聚焦**部署与运行时配置**的收敛方案,目标不是“更像生产”,而是“更适合当前阶段的单人维护”。

---

## 0. 这份文档解决什么问题

当前部署系统已经能工作,但存在一个很明显的阶段性矛盾:

- 架构上已经开始朝“生产形态”靠拢
- 维护方式仍然是**单人、高频调整、低运维预算**

在这个阶段,部署系统的首要目标不应该是“把所有生产治理项都堆上去”,而应该是:

1. **默认可用**
2. **容易理解**
3. **出问题容易排查**
4. **本地与生产不要维护两套心智模型**

换句话说,当前部署设计要服务于“个人可维护”,而不是提前模拟一个复杂团队的生产平台。

---

## 0.1 当前落地状态（2026-04-17）

本轮部署收敛已完成以下落地:

- `configs/*.prod.yaml` 已删除,服务只保留单份 YAML
- Compose 已通过 `init -> logic/task -> gateway -> web` 表达启动链路
- 默认 Compose 已删除 `deploy.resources`
- `services.prod.yaml` 仅保留端口关闭、Caddy 接入、Web runtime 地址注入
- 脚本已收口为校验 + compose 包装,不再依赖 `RESONANCE_ENV`

---

## 1. 改造前痛点

### 1.1 配置分叉过重

当前同时维护:

- `configs/{logic,gateway,task,web}.yaml`
- `configs/{logic,gateway,task,web}.prod.yaml`
- `.env`
- `deploy/services.yaml`
- `deploy/services.prod.yaml`

结果是“环境差异”被拆散到多个层次表达:

- YAML 文件表达一部分
- 环境变量表达一部分
- Compose override 再表达一部分

这对当前项目规模来说过重,也是维护成本的主要来源。

### 1.2 部署编排里有低收益复杂度

当前 Compose 里已经加入了不少更偏“生产治理”的配置:

- `deploy.resources`
- 本地/生产双脚本
- `Watchtower`
- 多层 compose 覆盖

这些能力不是错误,但在当前阶段的收益不高,却会显著增加理解成本和变更成本。

### 1.3 Bootstrap 存在“像服务,又不是服务”的尴尬

当前 `go run main.go -module init` 的职责是:

- `AutoMigrate`
- 默认房间初始化
- 管理员用户初始化
- 默认成员关系初始化

它本质上是一个**一次性任务**(one-shot job),不是长期服务。但如果它独立悬在部署流程之外,维护者会把它感知成“多了一个服务要单独操心”。

### 1.4 本地与生产的运行语义不够统一

例如:

- Docker 环境通过 hostname(`postgres`/`redis`)连接基础设施
- 本地开发通过 `127.0.0.1`
- 这种差异当前通过 `RESONANCE_ENV` + 双份 YAML 表达

这会让“环境”变成“切换一整套配置文件”的问题,而不是“覆盖少量运行时参数”的问题。

---

## 2. 设计目标

本轮部署设计只追求四件事:

### D1. 单一配置源优先

每个服务尽量只保留一份主配置文件。环境差异优先通过环境变量覆盖,而不是复制一份 `.prod.yaml`。

### D2. Compose 启动流程内建初始化

初始化任务可以存在,但不应该要求维护者手动记忆“先跑哪条命令再跑哪条命令”。Compose 应该把它纳入启动链路。

### D3. 默认简单,高级能力可选

像资源限制、自动更新、反代集成这类能力可以保留,但不应成为默认部署理解成本的一部分。

### D4. 不为当前阶段过早绑定基础设施语义

像 PostgreSQL init SQL、复杂多环境矩阵、生产治理参数这类能力,只有在边界稳定、收益明确时才引入。当前阶段优先保持系统易调整。

---

## 3. 边界判断

### 3.1 为什么不建议把现有 Bootstrap 直接塞进 PostgreSQL 初始化

PostgreSQL 自带的 `docker-entrypoint-initdb.d` 机制适合:

- 初次建库时执行固定 SQL
- 初始化 extension / schema / 少量静态数据

但它**不适合**当前项目里的 bootstrap 逻辑,因为当前逻辑不只是 SQL:

- 使用 `AutoMigrate(model.AllModels()...)`
- 创建默认房间
- 创建管理员账号
- 使用 `bcrypt` 生成密码
- 创建默认成员关系

这些都属于**应用层 bootstrap**,不是数据库容器本身应该负责的职责。

如果强行改成 PostgreSQL init SQL,会带来三个问题:

1. **只在空数据目录首次执行**,后续调整逻辑不会自动生效
2. **需要把 Go 逻辑拆成 SQL/脚本版本**,维护成本更高
3. **把数据库启动生命周期和业务初始化强耦合**,边界变差

结论:

- **不建议把当前 Go bootstrap 改写成 PostgreSQL init SQL**
- **建议保留 Go bootstrap,但把它编排进 Compose 启动流程**

### 3.2 Bootstrap 应该是什么

Bootstrap 在当前系统里的正确定位是:

- **one-shot job**
- **属于部署流程的一部分**
- **不是长期服务**

也就是说,它应该“存在于编排里”,但不应该“存在于维护者日常心智里”。

更合理的启动链路是:

1. `postgres` healthy
2. `init` 执行并成功退出
3. `logic` / `task` 启动
4. `gateway` / `web` 启动

这样维护体验上相当于“初始化与系统启动绑定在一起”,但职责边界仍然是正确的。

---

## 4. 目标部署形态

### 4.1 配置层

目标是:

- 每个服务只保留一份主配置:
  - `configs/logic.yaml`
  - `configs/gateway.yaml`
  - `configs/task.yaml`
  - `configs/web.yaml`
- 环境差异全部通过环境变量覆盖

不再依赖:

- `logic.prod.yaml`
- `gateway.prod.yaml`
- `task.prod.yaml`
- `web.prod.yaml`

环境差异应缩减为“覆盖少量关键运行时参数”,例如:

- 基础设施地址
- 公开域名
- Web runtime API/WS 地址
- 凭据/密钥

### 4.2 Compose 层

目标形态:

- 一份主 Compose 负责:
  - 基础设施
  - `init`
  - `logic`
  - `task`
  - `gateway`
  - `web`
- 如仍需生产覆盖,只保留一个**很薄**的 override,处理:
  - 是否暴露端口
  - 是否接入 Caddy
  - 是否启用自动更新

不应再让 override 承担大量运行时逻辑。

### 4.3 服务依赖链

推荐依赖关系:

- `init` 依赖 `postgres: healthy`
- `logic` / `task` 依赖 `init: completed successfully`
- `gateway` 依赖 `logic` 运行就绪的外部条件(或仅依赖基础设施健康)
- `web` 只依赖 `gateway` 可访问

这里的核心思想不是“所有服务都强依赖 init”,而是:

- **所有依赖数据库主事实的服务,都应该在初始化完成后再启动**

---

## 5. 应删减的部署复杂度

### 5.1 `deploy.resources`

当前阶段建议直接删除:

- CPU 限制
- 内存限制
- reservation

原因:

- 对单机 `docker compose` 的收益有限
- 增加阅读负担
- 容易制造“看起来更生产,实际上没人维护”的假复杂度

保留更有价值的项即可:

- `restart`
- `healthcheck`
- `depends_on`
- `volumes`
- `ports`
- `env_file/environment`

### 5.2 过多脚本分支

当前 `deploy-local.sh` 与 `deploy-production.sh` 不是不能存在,但不应承载太多“环境语义”。脚本应该只负责:

- 校验
- 包装 compose 命令
- 输出友好的启动信息

不应再让脚本承担一套“独立部署逻辑”。

### 5.3 `Watchtower`

`Watchtower` 可以保留,但不应成为默认部署路径的一部分。它更适合作为:

- 可选生产能力
- 非本地默认项

---

## 6. 不做的事情

本轮部署收敛明确**不做**以下事项:

1. **不把 Go bootstrap 改写为 PostgreSQL init SQL**
2. **不引入 Kubernetes / Helm / Swarm**
3. **不为 CPU/内存治理补更多参数**
4. **不把部署问题提前抽象进 Genesis**
5. **不同时支持过多部署模式**

目标是收敛,不是扩张。

---

## 7. 推荐推进顺序

按收益从高到低排序:

### Step 1 - 配置收敛

1. 删除 `configs/*.prod.yaml`
2. 保留单份 YAML
3. 将差异迁移到环境变量覆盖

验收:

- 本地与 Docker 都能启动
- 不再依赖 `RESONANCE_ENV=prod` 去切换整份配置文件

### Step 2 - `init` 编排化

1. 保留 `-module init`
2. 在 Compose 里把 `init` 明确为 one-shot job
3. 让依赖数据库主事实的服务等待 `init` 成功

验收:

- `docker compose up` 时不需要手工先跑初始化
- 重启业务服务时不会把 `init` 误当成长期服务看待

### Step 3 - 删除低收益治理项

1. 去掉 `deploy.resources`
2. 简化 `services.prod.yaml`
3. 将 `Watchtower` 继续留在可选生产 profile

验收:

- Compose 文件阅读成本显著下降
- 单人维护时不需要理解无关治理参数

### Step 4 - 脚本收口

1. 让脚本只做校验与调用包装
2. 不再承载环境逻辑分叉

验收:

- 任何部署命令都可以还原为一条清晰的 compose 命令

---

## 8. 目标状态总结

部署系统收敛后的理想状态是:

- **一套主 Compose**
- **一套主配置**
- **一个 one-shot init job**
- **极薄的生产覆盖**
- **最小必要的运行时参数**

维护者需要记住的只有:

1. 改 `.env` 覆盖运行时差异
2. 改 `configs/*.yaml` 调整服务默认行为
3. `docker compose up` 会自动跑初始化并拉起系统

这才符合当前项目阶段的真实需求。

---

## 9. 与现有文档的关系

- `00-overview.md` 定义服务边界。本文只讨论运行时收敛,不改变服务职责。
- `03-services.md` 定义代码组织。本文只讨论部署与配置,不改变目录结构。
- `05-migration.md` 定义业务架构迁移阶段。本文是部署层的配套收敛设计。
- `06-layout-refactor.md` 解决代码布局问题。本文解决部署心智负担问题。
