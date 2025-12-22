# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

此文件用于约束 Claude Code (claude.ai/code) 在 `Genesis` 仓库中的工作方式。请全程使用中文交流与记录，遵守下述行为准则，并对自己负责。

## 项目概览

`Genesis` 是一个 Go 语言微服务组件库，旨在沉淀可复用的基础设施组件。采用四层扁平化架构，通过显式依赖注入和Go原生设计，帮助开发者快速构建健壮、可维护的微服务应用。

**Genesis 不是框架**——我们提供积木，用户自己搭建。

## 项目架构

| 层次                        | 核心组件                                   | 职责                         | 组织方式 |
| :-------------------------- | :----------------------------------------- | :--------------------------- | :------- |
| **Level 3: Governance**     | `auth`, `ratelimit`, `breaker`, `registry` | 流量治理，身份认证，切面能力 | 扁平化   |
| **Level 2: Business**       | `cache`, `idgen`, `dlock`, `idempotency`, `mq` | 业务能力封装                 | 扁平化   |
| **Level 1: Infrastructure** | `connector`, `db`                          | 连接管理，底层 I/O           | 扁平化   |
| **Level 0: Base**           | `clog`, `config`, `metrics`, `xerrors`     | 框架基石                     | 扁平化   |

**设计原则**：显式优于隐式、简单优于聪明、组合优于继承
**依赖注入**：使用Go原生构造函数注入，已移除DI容器

## 技术栈

- **日志**: `slog` (标准库)
- **ORM**: `GORM` (数据库操作)
- **配置**: `Viper` (多源配置加载)
- **指标**: `OpenTelemetry` (可观测性)
- **缓存**: 支持 Redis
- **锁**: 支持 Redis/Etcd
- **消息队列**: 支持 NATS
- **数据库**: MySQL (支持分库分表)

## 常用开发命令

```bash
# 开发环境管理
make up          # 启动所有开发服务 (Redis, MySQL, Etcd, NATS)
make down        # 停止所有开发服务
make status      # 查看服务状态
make logs        # 显示所有服务日志

# 代码质量
make test        # 运行所有测试
make lint        # 运行代码检查 (golangci-lint)
make clean       # 清理卷和网络

# 示例运行
make examples    # 列出所有可用示例
make example-<component>  # 运行特定组件示例，如 make example-cache
make example-all # 运行所有示例
```

## 代码组织规范

### 扁平化结构

- **根目录** - 组件直接放在根目录，采用扁平化设计，不使用 `types/` 子包
- `internal/` - 内部实现逻辑，封装具体实现细节
- `examples/` - 使用示例，每个组件都有对应的可运行示例
- `docs/` - 设计文档，包括架构设计、组件规范和重构进度

### 组件初始化模式

使用Go原生显式依赖注入，遵循"谁创建，谁负责释放"原则：

```go
// 标准初始化模式
cfg, _ := config.Load("config.yaml")
logger, _ := clog.New(&cfg.Log)

redisConn, _ := connector.NewRedis(&cfg.Redis, connector.WithLogger(logger))
defer redisConn.Close()

cache, _ := cache.New(redisConn, &cfg.Cache, cache.WithLogger(logger))
```

### 资源所有权

- **Connector** - 拥有底层连接，负责Close()
- **Component** - 借用Connector，Close()通常是no-op
- **应用层** - 通过defer实现LIFO关闭顺序

### 基础设施组件使用规范

**⚠️ 重要原则：Genesis 所有组件必须使用 L0 基础设施组件**

Genesis 提供 4 个 L0 基础组件作为一切组件的基石。所有其他组件必须使用这些基础设施，禁止直接调用标准库。

#### 四个基础组件

1. **日志 (clog)** - 必须使用，禁止 `log.Printf`/`fmt.Printf`
2. **配置 (config)** - 必须使用，禁止直接解析环境变量
3. **错误 (xerrors)** - 必须使用，禁止 `errors.New`/`fmt.Errorf`
4. **指标 (metrics)** - 必须使用，禁止直接使用 Prometheus 客户端

#### 标准使用模式

```go
// ✅ 正确：使用 Genesis 基础组件
import "github.com/ceyewan/genesis/{clog,config,xerrors,metrics}"

// 组件接受依赖注入
func NewCache(conn redis.Conn, opts ...Option) (*Cache, error) {
    cache := &Cache{conn: conn}
    for _, opt := range opts { opt(cache) }

    // 默认值使用基础组件
    if cache.logger == nil {
        cache.logger = clog.Must(&clog.Config{Level: "info"})
    }
    return cache, nil
}

// 记录日志
cache.logger.Info("cache miss", clog.String("key", key))

// 错误处理
return nil, xerrors.Wrap(err, "connection failed")

// 配置解析
var cfg Config
loader.UnmarshalKey("cache", &cfg)

// 指标记录
cache.hits.Inc(ctx, metrics.L("type", "user"))
```

```go
// ❌ 禁止：直接使用标准库
import ("log"; "errors"; "fmt"; "os")

log.Printf("cache miss: %s", key)        // ❌
return fmt.Errorf("failed: %w", err)     // ❌
os.Getenv("CACHE_PREFIX")                // ❌
```

#### 依赖注入选项

```go
type Option func(*Cache)

func WithLogger(logger clog.Logger) Option {
    return func(c *Cache) { c.logger = logger }
}

func WithMeter(meter metrics.Meter) Option {
    return func(c *Cache) { c.meter = meter }
}

// 使用示例
cache, err := cache.New(redisConn, cfg,
    cache.WithLogger(logger),  // 注入日志
    cache.WithMeter(meter),    // 注入指标
)
```

## 重构进度追踪

项目已完成四层扁平化架构重构，所有组件均已符合 v0.1.0 发布标准。

**已完成重构的组件**：

- Level 0: clog, config, metrics, xerrors (✅)
- Level 1: connector, db (✅)
- Level 2: dlock, cache, idgen, mq (✅)
- Level 3: auth, ratelimit, breaker, registry (✅)

## 文档查阅指南

### 使用 go doc 查看组件文档

Genesis 项目强调"代码即注释"，所有组件都有完整的文档注释。遇到问题时，优先使用 `go doc` 查看文档：

```bash
# 查看组件的完整文档
go doc -all ./clog
go doc -all ./cache
go doc -all ./connector

# 查看特定类型或函数
go doc ./clog.New
go doc ./clog.WithNamespace
```

## 行为准则（编程版八荣八耻）

1. **以凭空猜测为耻，以查阅文档为荣**：对接口、配置、流程有疑问时，先读 `docs/`、根目录下的组件源码与注释。遇到不熟悉的组件，优先使用 `go doc -all ./<component>` 查看完整的 API 文档和使用示例。
2. **以模糊执行为耻，以确认反馈为荣**：不确定的改动先询问或验证，避免"差不多"式修改。
3. **以自说自话为耻，以对齐需求为荣**：实现前确认需求背景与边界，必要时向人类说明假设。
4. **以重复造轮为耻，以复用抽象为荣**：优先复用根目录中的接口和已存在的工具，避免新增无用抽象。
5. **以跳过验证为耻，以主动测试为荣**：改动后运行对应的测试或静态检查，确保功能与行为稳定。
6. **以破坏架构为耻，以遵循规范为荣**：保持四层扁平化架构，确保组件职责清晰，不将第三方依赖泄漏到组件。
7. **以假装理解为耻，以诚实求助为荣**：遇到不懂的概念或代码路径，坦诚指出并寻找答案。
8. **以鲁莽提交为耻，以谨慎重构为荣**：重构前先理解上下游调用，必要时分步骤进行并记录风险。

## Git 工作流

### 获取信息

通过 `git status` 查看当前分支状态，`git log --oneline` 查看提交历史，`git diff --cached` 查看未提交的改动，`git diff` 查看工作区与暂存区的差异，注意，不要使用交互式的命令。

### 分支命名

格式：`<type>/<description>[-suffix]`

类型：`feature` | `fix` | `refactor` | `docs` | `chore`

示例：`feature/idgen-implementation`、`fix/connection-timeout`

### 提交规范

格式：`<type>(<scope>): <subject>`

- **类型**：feat, fix, refactor, docs, style, test, chore
- **作用域**（可选）：如 clog, connector, cache 等
- **主题**：祈使语气，首字母小写，无句号
- **语言**：中文

如有多个逻辑变更，提供正文（用 `-` 列举）说明"做了什么"和"为什么"。

**示例**：

```
feat(clog): 添加错误堆栈跟踪和最佳实践文档

- 为 Error 和 ErrorWithCode 字段实现运行时堆栈跟踪收集
- 在设计文档中添加全面的最佳实践部分
- 修复自定义类型与 slog 级别之间的映射不一致问题
- 更新默认日志器配置，使用无颜色的控制台格式
```