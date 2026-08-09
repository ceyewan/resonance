# Agent Session Store and GC Verification

日期：2026-08-09

## 范围

本切片补齐 content-addressed Session Store 的孤儿对象回收和多实例串行化。Session JSONL 仍是不透明 Blob；GC 不解析其业务内容。

## 安全不变量

- 对象发布使用内容 SHA-256 命名，GC 删除前重新计算校验和；
- 只扫描 `objects/<前两位>/<sha256>.jsonl`，未知文件、软链接和异常目录不删除；
- live reference 同时包含所有 Tenant 的 Session binding，以及非终态 Run 的 candidate ref；
- PostgreSQL transaction advisory lock 在“读取引用集合 → 文件删除”整个窗口内持有；
- 同一共享 Session root 的多个 Pilot 只有一个实例执行 GC；
- 新对象必须超过 `gc_grace` 才能删除，覆盖 candidate publish 与数据库 prepare 之间的短暂窗口；
- 数据损坏、非法引用和数据库错误全部 fail closed，保留对象供隔离诊断。

## 可复现命令

```bash
GOCACHE=/tmp/resonance-gocache go test -race ./pilot/session -count=3
GOCACHE=/tmp/resonance-gocache go test ./repo \
  -run TestAgentRunRepo_SessionGCLockSnapshotsAllTenantReferencesAndSerializesCollectors \
  -count=1 -v
GOCACHE=/tmp/resonance-gocache go test ./pilot -count=1
```

Repository 用例使用 PostgreSQL 17 Testcontainers，验证跨 Tenant 引用快照、锁竞争时第二个 Collector 不执行回调，以及第一个事务结束后锁可再次获取。

## 结果

- Session Store race 三轮通过；
- PostgreSQL advisory-lock/引用快照测试通过；
- Pilot 生命周期测试证明 GC 在 Runtime probe 后启动，在停止摄入、排空 Runtime 后停止；
- 默认 `gc_interval=30m`、`gc_grace=24h`，配置校验要求 grace 至少是两倍 Run timeout。

## 部署边界

代码支持多个 Pilot 共享满足 POSIX 原子 link/rename 语义的持久卷，并用 PostgreSQL 串行 GC。当前 Compose 的 named volume 仅是单宿主实现；跨宿主或多可用区部署必须换成经过验证的共享 POSIX Store 或实现等价的对象存储 Adapter，不能把本地 named volume 宣称为跨主机高可用。
