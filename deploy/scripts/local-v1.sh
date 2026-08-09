#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
EXPECTED_SHA=69f02a11319e2adb58b20d7671647f523c18b8b2

case "${1:-}" in
  config)
    test "$(git rev-parse HEAD)" = "$EXPECTED_SHA"
    grep -Fq 'github.com/ceyewan/genesis v1.0.0-rc.1' go.mod
    ! grep -Eq '^replace[[:space:]]+github.com/ceyewan/genesis' go.mod
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
