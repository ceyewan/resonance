#!/usr/bin/env bash
# Validate or execute an atomic Agent control/runtime digest-pair rollback.
# The safe default only validates inputs. Actual mutation requires --execute.

set -euo pipefail

MODE="${1:---validate-only}"
if [[ "$MODE" != "--validate-only" && "$MODE" != "--execute" ]]; then
    echo "usage: $0 [--validate-only|--execute]" >&2
    exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

get_setting() {
    local key="$1"
    local from_environment="${!key:-}"
    if [[ -n "$from_environment" ]]; then
        printf '%s' "$from_environment"
        return
    fi
    if [[ ! -f .env ]]; then
        return
    fi
    local line
    line="$(grep -E "^${key}=" .env | tail -n1 || true)"
    printf '%s' "${line#*=}"
}

require_digest_ref() {
    local key="$1"
    local value="$2"
    if [[ ! "$value" =~ ^[^[:space:]@]+@sha256:[0-9a-f]{64}$ ]]; then
        echo "rollback refused: ${key} must be repository@sha256:<64 lowercase hex>" >&2
        exit 2
    fi
}

CONTROL_IMAGE="$(get_setting PILOT_PREVIOUS_IMAGE_DIGEST)"
RUNTIME_IMAGE="$(get_setting PILOT_RUNTIME_PREVIOUS_IMAGE_DIGEST)"
require_digest_ref PILOT_PREVIOUS_IMAGE_DIGEST "$CONTROL_IMAGE"
require_digest_ref PILOT_RUNTIME_PREVIOUS_IMAGE_DIGEST "$RUNTIME_IMAGE"
if [[ "$CONTROL_IMAGE" == "$RUNTIME_IMAGE" ]]; then
    echo "rollback refused: control and runtime image references must be distinct" >&2
    exit 2
fi

echo "validated Agent rollback pair:"
echo "  control: $CONTROL_IMAGE"
echo "  runtime: $RUNTIME_IMAGE"
if [[ "$MODE" == "--validate-only" ]]; then
    exit 0
fi

if [[ ! -f .env ]]; then
    echo "rollback refused: project .env is required for Compose execution" >&2
    exit 2
fi
command -v docker >/dev/null 2>&1 || {
    echo "rollback refused: docker is unavailable" >&2
    exit 2
}
docker compose version >/dev/null

EVIDENCE_PATH="${AGENT_ROLLBACK_EVIDENCE:-agent-rollback-evidence-$(date -u +%Y%m%dT%H%M%SZ).json}"
EVIDENCE_DIR="$(dirname "$EVIDENCE_PATH")"
if [[ ! -d "$EVIDENCE_DIR" || ! -w "$EVIDENCE_DIR" || -e "$EVIDENCE_PATH" ]]; then
    echo "rollback refused: evidence target must have a writable parent and must not already exist: $EVIDENCE_PATH" >&2
    exit 2
fi

export RESONANCE_PILOT_IMAGE="$CONTROL_IMAGE"
export RESONANCE_PILOT_RUNTIME_IMAGE="$RUNTIME_IMAGE"
COMPOSE=(
    docker compose --env-file .env -p resonance
    -f deploy/base.yaml
    -f deploy/services.yaml
    -f deploy/services.prod.yaml
    --profile production
)
"${COMPOSE[@]}" config -q

MUTATION_STARTED=false
ROLLBACK_COMPLETE=false
on_exit() {
    local status=$?
    if [[ "$MUTATION_STARTED" == "true" && "$ROLLBACK_COMPLETE" != "true" ]]; then
        echo "rollback did not complete; keeping both Agent controls stopped" >&2
        "${COMPOSE[@]}" stop pilot pilot-iam-admin >/dev/null 2>&1 || true
    fi
    exit "$status"
}
trap on_exit EXIT

wait_healthy() {
    local container="$1"
    local timeout_seconds="${2:-180}"
    local deadline=$((SECONDS + timeout_seconds))
    local status
    while (( SECONDS < deadline )); do
        status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || true)"
        if [[ "$status" == "healthy" ]]; then
            return 0
        fi
        if [[ "$status" == "exited" || "$status" == "dead" ]]; then
            echo "rollback failed: $container entered $status" >&2
            return 1
        fi
        sleep 2
    done
    echo "rollback failed: $container did not become healthy within ${timeout_seconds}s" >&2
    return 1
}

assert_image_ref() {
    local container="$1"
    local expected="$2"
    local actual
    actual="$(docker inspect --format '{{.Config.Image}}' "$container")"
    if [[ "$actual" != "$expected" ]]; then
        echo "rollback failed: $container uses $actual, expected $expected" >&2
        return 1
    fi
}

echo "pulling immutable rollback images"
docker pull "$CONTROL_IMAGE"
docker pull "$RUNTIME_IMAGE"

echo "stopping Agent ingress before replacing runtimes"
MUTATION_STARTED=true
"${COMPOSE[@]}" stop pilot pilot-iam-admin

echo "restoring profile runtimes"
"${COMPOSE[@]}" up -d --no-deps pilot-runtime pilot-iam-admin-runtime
wait_healthy resonance-pilot-runtime
wait_healthy resonance-pilot-iam-admin-runtime
assert_image_ref resonance-pilot-runtime "$RUNTIME_IMAGE"
assert_image_ref resonance-pilot-iam-admin-runtime "$RUNTIME_IMAGE"

echo "restoring Agent controls"
"${COMPOSE[@]}" up -d --no-deps pilot pilot-iam-admin
wait_healthy resonance-pilot
wait_healthy resonance-pilot-iam-admin
assert_image_ref resonance-pilot "$CONTROL_IMAGE"
assert_image_ref resonance-pilot-iam-admin "$CONTROL_IMAGE"

COMPLETED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
umask 077
printf '{\n  "completed_at": "%s",\n  "control_image": "%s",\n  "runtime_image": "%s",\n  "runtime_ready": true,\n  "control_ready": true\n}\n' \
    "$COMPLETED_AT" "$CONTROL_IMAGE" "$RUNTIME_IMAGE" > "$EVIDENCE_PATH"
ROLLBACK_COMPLETE=true
echo "Agent digest-pair rollback completed; evidence: $EVIDENCE_PATH"
echo "the operator must still record the old-Session fixture result before restoring broader tenant admission"
