#!/usr/bin/env bash
# 幂等安装 protobuf 代码生成器（本地插件，不走 BSR 远端，无需登录与网络配额）。
#
# 版本与 api/buf.gen.*.yaml、go.mod、web/package.json 一一对应：
#   protoc-gen-go        v1.36.11  == go.mod google.golang.org/protobuf
#   protoc-gen-go-grpc   v1.5.1    == api/buf.gen.go.yaml 注释
#   protoc-gen-connect-go v1.19.1  == go.mod connectrpc.com/connect
#   protoc-gen-es        v2.11.0   == web/package.json @bufbuild/protobuf
#
# 重复执行只会补齐缺失或版本不符的插件；已就绪时直接退出 0。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
GOBIN=$(go env GOPATH)/bin

PROTOC_GEN_GO_VERSION="v1.36.11"
PROTOC_GEN_GO_GRPC_VERSION="v1.5.1"
PROTOC_GEN_CONNECT_GO_VERSION="v1.19.1"
PROTOC_GEN_ES_VERSION="2.11.0"

# plugin 名 -> "模块@版本"
declare -a GO_PLUGINS=(
  "protoc-gen-go=google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}"
  "protoc-gen-go-grpc=google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}"
  "protoc-gen-connect-go=connectrpc.com/connect/cmd/protoc-gen-connect-go@${PROTOC_GEN_CONNECT_GO_VERSION}"
)

version_of() {
  # 各插件的 --version 输出格式不同（有的带插件名，有的不带 v 前缀），统一取最后一段。
  "$1" --version 2>/dev/null | awk '{print $NF}' | sed 's/^v//' || true
}

install_go_plugins() {
  for entry in "${GO_PLUGINS[@]}"; do
    local name="${entry%%=*}"
    local module="${entry#*=}"
    local want="${module##*@}"
    want="${want#v}"
    local path="${GOBIN}/${name}"
    if [[ -x "${path}" ]] && [[ "$(version_of "${path}")" == "${want}" ]]; then
      echo "[gen-tools] ${name} ${want} 已就绪"
      continue
    fi
    echo "[gen-tools] 安装 ${name} ${want} ..."
    GOBIN="${GOBIN}" go install "${module}"
    local got
    got="$(version_of "${path}")"
    if [[ "${got}" != "${want}" ]]; then
      echo "[gen-tools] ${name} 版本不符：期望 ${want}，实际 ${got}" >&2
      exit 1
    fi
  done
}

install_es_plugin() {
  local bin="${ROOT}/tools/node_modules/.bin/protoc-gen-es"
  local want="${PROTOC_GEN_ES_VERSION}"
  if [[ -x "${bin}" ]] && [[ "$(version_of "${bin}")" == "${want}" ]]; then
    echo "[gen-tools] protoc-gen-es ${want} 已就绪"
    return
  fi
  echo "[gen-tools] 安装 protoc-gen-es ${want}（tools/）..."
  cd "${ROOT}/tools"
  npm ci --silent
  local got
  got="$(version_of "${bin}")"
  if [[ "${got}" != "${want}" ]]; then
    echo "[gen-tools] protoc-gen-es 版本不符：期望 ${want}，实际 ${got}" >&2
    exit 1
  fi
}

main() {
  if ! command -v buf >/dev/null 2>&1; then
    echo "[gen-tools] 缺少 buf（>=1.66.1），请先安装：https://buf.build/docs/installation" >&2
    exit 1
  fi
  if ! command -v go >/dev/null 2>&1; then
    echo "[gen-tools] 缺少 go 工具链" >&2
    exit 1
  fi
  install_go_plugins
  install_es_plugin
  echo "[gen-tools] 全部生成器就绪：${GOBIN}、${ROOT}/tools/node_modules/.bin"
}

main "$@"