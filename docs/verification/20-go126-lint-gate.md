# Go 1.26 Lint 门禁验证

日期：2026-08-09

## 目标

证明仓库的 Go 静态检查实际加载全部 package，而不是因为检查器不支持 Go 1.26
而在 package loading 阶段失效。CI 和本地入口必须使用同一个固定版本，配置错误或
版本漂移均应显式失败。

## 修复边界

- GitHub Actions 固定 `golangci-lint v2.12.2`，使用 v2 module path 安装；
- `.golangci.yaml` 迁移到 v2 schema，并保持 `errcheck`、`govet`、
  `staticcheck`、`unused`、`unparam`、`gofmt` 和 `goimports` 等现有门禁；
- `make lint-go` 在执行前核对精确版本，避免 PATH 中残留的 v1 二进制产生误导结果；
- 只对测试夹具的固定参数排除 `unparam`。生产代码和资源关闭错误没有全局豁免。

迁移后首次真实加载全仓时发现并修复了历史关闭错误、无效赋值、冗余 Runtime 返回值、
租户校验未使用结果、错误文案和 import 排序。检查器最终报告 `0 issues`。

## 可重现命令

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

GOCACHE=/tmp/resonance-gocache \
GOLANGCI_LINT_CACHE=/tmp/resonance-golangci-lint-cache \
  make lint-go

GOCACHE=/tmp/resonance-gocache go test ./... -run '^$'
GOCACHE=/tmp/resonance-gocache go vet ./...

GOCACHE=/tmp/resonance-gocache go test -race \
  ./logic/service ./pilot/runtime/pi ./pilot/coordinator ./pilot/mutation \
  ./pilot/logicclient ./pilot/eval ./pilot/observability \
  ./gateway/transport/ws ./task/streaming -count=3
```

本轮全仓 lint、编译、Vet、目标功能测试和上述 3 轮 Race 均通过；Buf、Markdown、
Prettier 与 `git diff --check` 也通过。

## CI 解释

固定检查器版本是可重现发布门禁的一部分。升级 Go 版本时必须先确认静态检查器支持该
版本并真实加载 package；不能把“命令退出”或空结果当作 lint 通过。升级检查器时需要
同时迁移配置、审查新增诊断并记录验证证据。
