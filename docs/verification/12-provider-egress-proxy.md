# Provider CONNECT Egress Proxy 验证

## 1. 本切片边界

独立的 `egress-proxy` 进程只负责把经过严格校验的 TLS CONNECT 隧道转发给 Provider。它不实现透明代理或 TLS MITM，也不持有 Provider Key。

最终 Compose 拓扑已经接通：

- `runtime-internal` 设置 `internal: true`；
- proxy 同时加入 `runtime-internal` 和独立的 `provider-egress` 网络；
- Pi/Node/Bridge 只存在于 profile-specific Runtime sidecar，并且只加入 `runtime-internal`；
- Go Pilot 控制面只加入 `resonance-net`，通过共享私有 UDS volume 调用 Runtime；
- proxy 是唯一同时加入 `runtime-internal` 与 `provider-egress` 的进程；
- Runtime 不挂 `resonance-net`，不能直达 PostgreSQL、NATS、Etcd 或 Logic；控制面不接收 Provider Key。

Runtime/UDS/凭证分离的完整验收见 `13-agent-runtime-isolation.md`。

## 2. Fail-closed 契约

- 仅接受 HTTP/1.1 CONNECT。普通 HTTP、请求体、userinfo、IP literal、通配域、尾点、大小写或端口非规范形式均拒绝。
- 生产配置显式只允许 lower-case ASCII IDNA A-label `api.anthropic.com` 和端口 `443`。allowlist 为空、重复或包含非规范项时启动失败。
- DNS 由 proxy 自己解析；任何候选地址属于 private、loopback、link-local、multicast、unspecified、documentation、benchmark、metadata、IPv4-mapped 或受限转换网段时，整个答案集都拒绝。
- dial 只接收已经校验过的 IP，不再次传入 hostname，避免 DNS rebinding。
- CONNECT 200 后有界读取一个完整 TLS ClientHello，支持 TCP 和 TLS record 分片。SNI 必须是与 CONNECT host 完全一致的 lower-case ASCII A-label；ALPN 必须存在且只能包含 `h2`、`http/1.1`。
- ClientHello 原始 record 校验后原样转发；代理不终止 TLS、不读取 HTTP header，也不记录目标请求中的 header、token 或 secret。
- DNS、dial、HTTP header、ClientHello、idle 和最大连接时长均有 timeout；全局连接数和每来源实例连接数都有上限。关闭服务会同步关闭 hijacked tunnel。

## 3. 测试覆盖

```bash
go test ./pilot/egressproxy -count=1 -v
go test -race ./pilot/egressproxy -count=1
go vet ./pilot/egressproxy
go test . -run '^$' -count=1
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml config
make lint-markdown
git diff --check
```

真实 loopback 测试使用本机临时 TCP 端口建立 CONNECT，会验证：

- DNS 返回的已验证 IP 被直接交给 dialer，hostname 不会被二次解析；
- 分片 ClientHello 能通过，校验后的 TLS bytes 和后续 opaque bytes 双向原样转发；
- 普通 HTTP、IP literal、私网/metadata/mixed DNS、host/port 变形在 dial 前拒绝；
- DNS、dial、ClientHello、idle、并发和服务关闭均能有界回收连接。

纯协议测试额外覆盖无 SNI、错误/非规范 SNI、无 ALPN、非法 ALPN、非 TLS、超大 ClientHello、IPv4-mapped 与 documentation 地址。

Compose 展开还必须确认环境变量与网络集合，而不是只确认 YAML 能解析：

```text
pilot networks:        resonance-net
pilot-runtime networks: runtime-internal
proxy networks:        runtime-internal, provider-egress
pilot-runtime env:     Provider Key + HTTP(S)_PROXY + offline/telemetry flags
pilot env:             DB/NATS/Etcd + service/capability identity，绝无 Provider Key
```
