# Agent Runtime Sidecar 隔离验证

## 目标

Go Pilot 继续拥有 durable queue、租约、预算、Session commit、Tool Broker 与 Logic 调用；不受信任面更大的 Node/Pi/Bridge 放入 profile-specific Runtime sidecar。两者只通过权限为 `0600` 的私有 Unix Domain Socket 通信，不对容器网络或公网暴露 Pi RPC。

这仍然是“Go 控制面 + Pi Harness Runtime”，不是 Docker Agent，也不是面向客户端的远程 Pi 服务。`AgentRuntime` 之上的业务代码不知道本地子进程或 UDS transport。

## 发布与信任边界

| 进程 | 镜像内容 | 凭证 | 网络 |
| --- | --- | --- | --- |
| Pilot control | Go binary，无 Node/Pi | PostgreSQL/NATS/Etcd、Logic service-auth、Capability signing key | `resonance-net` |
| Runtime sidecar | Go Runtime host、Node 22、Pi 0.84.1、可信 Bridge | Provider API Key | `runtime-internal` |
| Egress proxy | Go CONNECT proxy | 无 Provider/API/业务凭证 | `runtime-internal` + `provider-egress` |

user-assistant 与 iam-admin 使用独立 Runtime、socket volume、Session volume、Pilot workload identity 与 Capability key。Runtime 容器不挂 Docker Socket、宿主 Home 或 `~/.pi`，以固定 uid 10001、只读 rootfs、全部 capability drop 和 `no-new-privileges` 运行。

## UDS 与生命周期契约

- `pilot/runtime/remote` 使用有界 HTTP-over-UDS，仅暴露 Run、Abort、Probe、Shutdown；协议版本、请求、Frame、事件队列和 Session root 都有硬限额。
- socket parent 必须是无 symlink 的 `0700` 目录，socket 为 `0600`；启动绝不覆盖普通文件。
- Control 只能提交 Runtime Session root 内的绝对 clean path；Pi Adapter 再拒绝 symlink、hardlink、FIFO 和不安全权限。
- Tool Broker 也只监听 control/runtime 共享 volume 中的 UDS。Runtime 内的 loopback Relay 把 Bridge 固定的 HTTP endpoint 转发到该 UDS，不接受任意 upstream。
- Control 调用 Shutdown 后，Runtime host 立即撤销 readiness 并正常退出；后续 Control 重启不会连到一个已关闭却仍报健康的 Adapter。
- Run transport 在请求可能已到达后断开时返回 `UNKNOWN`，预算 reservation 保留；只有明确未发送 Prompt 才返回 `NOT_STARTED`。
- Bridge 只在预算 Hook、Manifest 和全部业务 Tool 注册完成后发布 `resonance_bridge_ready`；Adapter 在 Prompt 前核验 profile/version/tool count。Pi 0.84.1 固有的隐藏 `llama` provider command 以精确名称/描述单独 allowlist，任何其他 extension command 都拒绝；Runtime 不注入 `LLAMA_*` 且网络不能到达任意 llama endpoint。
- Pi 配置目录由 Runtime host 固定为私有 `PI_CODING_AGENT_DIR`；启动时原子恢复可信 retry policy，每个 Run 前再次按字节核验。Provider SDK 内部 retry 为 0，只有会重新经过预算 Hook 的 Agent 外层 retry 可以继续。

## 验证证据

```bash
go test -race ./pilot/runtime/remote ./pilot/runtime/relay ./pilot/toolbroker ./pilot/runtimehost -count=10

RESONANCE_PI_BINARY="$PWD/pilot/bridge/node_modules/.bin/pi" \
RESONANCE_PI_EXPECTED_VERSION=0.84.1 \
RESONANCE_PI_BRIDGE="$PWD/pilot/bridge/src/index.ts" \
go test -tags=pi_contract ./pilot/runtime/pi -run TestRealPiRPCContract -count=1 -v

docker build --target pilot-control-final -t resonance-pilot:verify -f deploy/Dockerfile .
docker build --target pilot-runtime-final -t resonance-pilot-runtime:verify -f deploy/Dockerfile .
docker image inspect resonance-pilot-runtime:verify --format '{{.Config.User}}'
docker run --rm --entrypoint /opt/resonance/bridge/node_modules/.bin/pi \
  resonance-pilot-runtime:verify --version
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml config -q
```

固定镜像验收值为用户 `resonance`、Pi `0.84.1`；control 镜像内必须找不到 `node` 和 Pi binary。真实 Pi 契约离线验证 `--mode rpc`、固定安全 flags、`get_state`、`get_session_stats`、Abort 和 stdin close，不产生 Provider 请求。

## 已知边界

静态 path 检查不能完全消除传统的 check/open TOCTOU。当前容器 mount namespace、私有 profile volume、同 UID、无宿主挂载和 Pi Adapter 的逐段检查共同缩小风险；若未来允许不受信任容器共享该 volume，Session Store 必须改用 `openat`/`O_NOFOLLOW` 或等价 dirfd API。跨主机部署还必须把本地 named volume 替换为经过原子 publish/CAS 验证的共享 Store。
