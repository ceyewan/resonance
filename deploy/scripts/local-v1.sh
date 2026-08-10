#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
BASELINE_SHA=d4fd1d1aef103a7d18353d5957aca541cfba884d
export GOWORK=off
export RESONANCE_VERSION=${RESONANCE_VERSION:-$(git rev-parse --short=12 HEAD)}

case "${1:-}" in
  config)
    git merge-base --is-ancestor "$BASELINE_SHA" HEAD
    deploy/scripts/verify-genesis-rc2-identity.sh >/dev/null
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
