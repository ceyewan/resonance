#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
export GOWORK=off

command -v gh >/dev/null 2>&1 || { echo "gh is required to bind Hosted CI evidence" >&2; exit 2; }
branch=$(git symbolic-ref --quiet --short HEAD || true)
head_sha=$(git rev-parse HEAD)
origin_main_sha=$(git rev-parse origin/main)
remote_main_sha=$(gh api repos/ceyewan/resonance/git/ref/heads/main --jq '.object.sha')
[[ "$branch" == "main" && "$head_sha" == "$origin_main_sha" && "$head_sha" == "$remote_main_sha" ]] || {
  echo "final Stage 3 evidence must run from synchronized main" >&2
  exit 1
}
[[ -z "$(git status --short)" ]] || {
  echo "final Stage 3 evidence requires a clean worktree" >&2
  exit 1
}
[[ -z "$("${COMPOSE[@]}" ps -q)" ]] || {
  echo "final Stage 3 evidence must start from a stopped resonance-v1 project" >&2
  exit 1
}

deploy/scripts/verify-genesis-rc2-identity.sh >/dev/null
run_id=${RESONANCE_STAGE3_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}
[[ "$run_id" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || { echo "invalid Stage 3 run id" >&2; exit 2; }
run_root="artifacts/local-v1/rc2-adoption-final/$run_id"
[[ ! -e "$run_root" ]] || { echo "Stage 3 run already exists: $run_root" >&2; exit 1; }
mkdir -p "$run_root"
run_root=$(cd "$run_root" && pwd)
export RESONANCE_VERSION=${head_sha:0:12}

cleanup() {
  deploy/scripts/local-v1.sh down >/dev/null 2>&1 || true
}
trap cleanup EXIT

deploy/scripts/local-v1.sh up
EVIDENCE_DIR="$run_root/verify" deploy/scripts/verify-local-v1.sh
EVIDENCE_DIR="$run_root/recovery" deploy/scripts/recovery-local-v1.sh
ALERT_EVIDENCE_DIR="$run_root/alerts" deploy/scripts/alerts-local-v1.sh
BENCHMARK_OUT_DIR="$run_root/benchmark" deploy/scripts/benchmark-local.sh
"${COMPOSE[@]}" images --format json | jq '
  map({
    service:(.ContainerName | sub("^resonance-v1-"; "") | sub("-[0-9]+$"; "")),
    container:.ContainerName,
    image:.Repository,
    tag:.Tag,
    id:.ID,
    platform:.Platform,
    size:.Size
  })
' >"$run_root/image-identities.json"
deploy/scripts/local-v1.sh down

gh api -H 'Accept: application/vnd.github+json' \
  "repos/ceyewan/resonance/commits/$head_sha/check-runs?per_page=100" \
  | jq '{total_count,checks:[.check_runs[] | {name,status,conclusion,details_url,head_sha}]}' \
  >"$run_root/hosted-ci.json"
jq -e --arg sha "$head_sha" '
  .total_count == 9 and (.checks | length) == 9 and
  all(.checks[]; .head_sha == $sha and .status == "completed" and .conclusion == "success")
' "$run_root/hosted-ci.json" >/dev/null

deploy/scripts/verify-genesis-rc2-identity.sh >"$run_root/genesis-rc2-identity.json"
shasum -a 256 deploy/base.yaml deploy/services.yaml deploy/services.local.yaml deploy/observability.yaml \
  >"$run_root/compose-files.sha256"
host_kernel=$(uname -a)
go_version=$(go version)
git_version=$(git version)
docker_client_version=$(docker version --format '{{.Client.Version}}')
docker_server_version=$(docker version --format '{{.Server.Version}}')
docker_context=$(docker context show)
jq -n \
  --arg host_kernel "$host_kernel" \
  --arg go_version "$go_version" \
  --arg git_version "$git_version" \
  --arg docker_client_version "$docker_client_version" \
  --arg docker_server_version "$docker_server_version" \
  --arg docker_context "$docker_context" \
  '{schema_version:1,host_kernel:$host_kernel,go_version:$go_version,git_version:$git_version,docker:{client_version:$docker_client_version,server_version:$docker_server_version,context:$docker_context}}' \
  >"$run_root/environment.json"
deploy/scripts/check-evidence-secrets.sh "$run_root" >"$run_root/sensitive-field-scan.log"
(cd "$run_root" && find . -type f ! -name bundle.sha256 -print | LC_ALL=C sort | while IFS= read -r evidence_file; do
  shasum -a 256 "$evidence_file"
done) >"$run_root/bundle.sha256"
bundle_manifest_sha=$(shasum -a 256 "$run_root/bundle.sha256" | awk '{print $1}')

evidence_path=docs/verification/evidence/resonance-stage3-genesis-v1.0.0-rc.2.json
jq -n \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg run_id "$run_id" \
  --arg tested_resonance_sha "$head_sha" \
  --arg evidence_locator "artifacts/local-v1/rc2-adoption-final/$run_id" \
  --arg bundle_manifest_sha256 "$bundle_manifest_sha" \
  '{
    schema_version:"resonance-stage3-rc2-adoption/v1",status:"PASS",generated_at:$generated_at,run_id:$run_id,
    tested_resonance_sha:$tested_resonance_sha,
    genesis_rc2:{version:"v1.0.0-rc.2",tag_object:"c759bb0b961bbdd685f5176520fea872084dbe17",source_commit:"f78d7860849019ae5a35c6473420b5e7db2269a0",sum:"h1:YtB2IJHqJ5ZucCDL7KfDPU3pyM9/yAotI0xUNlFEIaA=",go_mod_sum:"h1:Uysrd3364pkU2OguYEWKyMVkgGvq3x/4dpC/QD8v8OA="},
    evidence:{locator:$evidence_locator,bundle_manifest_sha256:$bundle_manifest_sha256},
    checks:{hosted_ci_9_of_9:true,started_from_stopped:true,identity_gate:true,compose_e2e:true,telemetry_correlation:true,recovery:true,alerts:true,benchmark:true,sensitive_scan:true},
    invalidation_rules:[
      "tested_resonance_sha changes",
      "Genesis RC2 module version or checksum changes",
      "any Compose input file changes",
      "any image identity changes",
      "the evidence bundle or locator becomes unavailable",
      "any required Hosted CI check is not successful"
    ]
  }' >"$evidence_path"

echo "$run_root"
echo "$evidence_path"
