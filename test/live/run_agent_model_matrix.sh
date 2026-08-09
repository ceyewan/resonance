#!/bin/bash
# Run the paid Agent smoke E2E against one or more DashScope models in a local,
# disposable deployment, then restore the
# model configured in the ignored repository .env. Provider credentials remain
# inside the Runtime containers and are never read or printed by this script.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

if [[ ! -f .env ]]; then
    echo "error: .env is required" >&2
    exit 1
fi

DEFAULT_MODEL="$(awk -F= '$1=="DASHSCOPE_MODEL" {print substr($0, index($0,"=")+1)}' .env | tail -n1)"
if [[ ! "$DEFAULT_MODEL" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "error: DASHSCOPE_MODEL is missing or invalid" >&2
    exit 1
fi

COMPOSE=(docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml)
if (( $# == 0 )); then
    MODELS=(qwen3.7-plus qwen3.7-flash)
else
    MODELS=("$@")
fi

activate_model() {
    local model="$1"
    DASHSCOPE_MODEL="$model" "${COMPOSE[@]}" up -d --force-recreate --wait --wait-timeout 120 \
        pilot-runtime pilot

    local control_model runtime_model
    control_model="$("${COMPOSE[@]}" exec -T pilot printenv RESONANCE_PROFILE_MODEL)"
    runtime_model="$("${COMPOSE[@]}" exec -T pilot-runtime printenv DASHSCOPE_MODEL)"
    if [[ "$control_model" != "$model" || "$runtime_model" != "$model" ]]; then
        echo "error: requested model was not applied to both control and Runtime" >&2
        exit 1
    fi
}

restore_default() {
    echo "Restoring Agent model: $DEFAULT_MODEL"
    activate_model "$DEFAULT_MODEL"
}
trap restore_default EXIT

for model in "${MODELS[@]}"; do
    if [[ ! "$model" =~ ^[A-Za-z0-9._-]+$ ]]; then
        echo "error: invalid model name: $model" >&2
        exit 1
    fi
    echo "Running live Agent E2E with model: $model"
    activate_model "$model"
    RESONANCE_LIVE_AGENT_E2E=1 RESONANCE_LIVE_EXPECTED_MODEL="$model" \
        go test ./test/live -run '^TestAgentServiceDashScope' -v -count=1
done
