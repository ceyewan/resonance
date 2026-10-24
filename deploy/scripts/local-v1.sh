#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
export GOWORK=off
export RESONANCE_VERSION=${RESONANCE_VERSION:-$(git rev-parse --short=12 HEAD)}

case "${1:-}" in
  config)
    "${COMPOSE[@]}" config --quiet
    ;;
  up)
    "$0" config
    "${COMPOSE[@]}" up -d --build
    ;;
  down)
    "${COMPOSE[@]}" down
    ;;
  *)
    echo "usage: $0 {config|up|down}" >&2
    exit 2
    ;;
esac
