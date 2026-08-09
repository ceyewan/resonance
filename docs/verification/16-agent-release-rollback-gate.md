# Agent 发布与回滚门禁验证

日期：2026-08-09

## 目标

把 Agent control/runtime 作为不可拆分的发布单元，防止生产部署或紧急回滚使用空值、浮动 tag、非法 digest、不匹配的镜像或复用的工作负载 Secret。

本记录验证机器可执行的发布和回滚门禁，不把脚本校验冒充为候选环境的实际回滚演练。

## 发布产物不变量

GitHub tag 发布同时构建 control 和 Runtime，并生成 `agent-release-<tag>` artifact。其中记录：

- `RESONANCE_PILOT_IMAGE=repository@sha256:<64 hex>`；
- `RESONANCE_PILOT_RUNTIME_IMAGE=repository@sha256:<64 hex>`；
- Pi、Bridge 和 Remote Runtime 协议版本；
- Git source ref 与 source SHA。

两个镜像同时发布 BuildKit SBOM 和 provenance attestation。Artifact 只是一组兼容引用，不包含 Provider 或业务 Secret。

## 生产准入

`deploy/scripts/deploy-production.sh` 在联网拉取或变更容器前执行以下检查：

1. control/runtime 都是小写十六进制的不变 digest 引用；
2. Gateway、两个 Pilot 的 Capability/service-auth Secret 均已配置、长度至少 32 且互不复用；
3. Provider Key 已配置，不是仓库占位符；
4. 主应用 tag 符合 Docker tag 语法；
5. Compose 启动使用 `--wait` 和有界超时，不在 not-ready 状态报告部署成功。

## 回滚状态机

`deploy/scripts/rollback-agent.sh` 默认为 `--validate-only`，必须显式使用 `--execute` 才会变更环境。执行顺序固定为：

1. 验证 previous control/runtime digest 对、Compose 和不覆盖的证据路径；
2. 先拉取两个不变镜像；
3. 停止两个 Pilot control，从而停止新 ingress/claim；
4. 恢复两个 Runtime，等待 healthy 并核对容器实际 image ref；
5. 恢复两个 control，等待 healthy 并再次核对 image ref；
6. 以 `0600` 新建 JSON 证据，记录完成时间和 digest 对。

从第 3 步起任意错误都会触发失败 Trap，将两个 control 保持在停止状态，避免只有一个 Profile 在部分回滚后继续接收新 Run。

## 可重现命令

```bash
bash -n deploy/scripts/deploy-production.sh deploy/scripts/rollback-agent.sh

PILOT_PREVIOUS_IMAGE_DIGEST="registry.example/resonance-pilot@sha256:$(printf 'a%.0s' {1..64})" \
PILOT_RUNTIME_PREVIOUS_IMAGE_DIGEST="registry.example/resonance-pilot-runtime@sha256:$(printf 'b%.0s' {1..64})" \
  ./deploy/scripts/rollback-agent.sh --validate-only

docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml config -q
make lint-markdown
git diff --check
```

CI 还会主动传入 `:latest` 并要求校验失败，防止后续变更把浮动 tag 当成可回滚事实。

## 已证明与未证明

已证明：脚本语法、digest 正/反例、发布产物字段、Compose 结构、失败后停止摄入的实现路径，以及文档/格式门禁。

尚未证明：在真实候选环境中，将新 Runtime 升级故障后切回上一个不同的 control/runtime digest 组合，并成功继续读取旧 Session fixture。当前本地没有两套可区分的已发布镜像，不使用重打 tag 的同一镜像伪造该证据。因此 `DEVELOPMENT.md` 中候选环境实操项保持未完成。
