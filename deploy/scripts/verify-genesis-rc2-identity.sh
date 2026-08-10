#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

readonly EXPECTED_VERSION=v1.0.0-rc.2
readonly EXPECTED_SUM='h1:YtB2IJHqJ5ZucCDL7KfDPU3pyM9/yAotI0xUNlFEIaA='
readonly EXPECTED_GO_MOD_SUM='h1:Uysrd3364pkU2OguYEWKyMVkgGvq3x/4dpC/QD8v8OA='

command -v go >/dev/null 2>&1 || { echo "go is required" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

# Stage 3 must exercise the public RC2 module. A workspace, replacement, or
# repository-local Genesis tree would make the evidence describe different
# source code even if go.mod still displayed the expected version.
export GOWORK=off
[[ ! -e go.work && ! -e go.work.sum ]] || {
  echo "go.work/go.work.sum is forbidden for the RC2 adoption gate" >&2
  exit 1
}
[[ ! -d genesis ]] || {
  echo "repository-local Genesis source is forbidden for the RC2 adoption gate" >&2
  exit 1
}

module_edit=$(go mod edit -json)
test "$(jq '.Replace | length' <<<"$module_edit")" -eq 0 || {
  echo "module replacements are forbidden for the RC2 adoption gate" >&2
  exit 1
}

module=$(go list -m -json github.com/ceyewan/genesis)
download=$(go mod download -json "github.com/ceyewan/genesis@$EXPECTED_VERSION")
jq -e --arg version "$EXPECTED_VERSION" '
  .Path == "github.com/ceyewan/genesis" and
  .Version == $version and
  (.Replace == null)
' <<<"$module" >/dev/null
jq -e --arg version "$EXPECTED_VERSION" --arg sum "$EXPECTED_SUM" --arg go_mod_sum "$EXPECTED_GO_MOD_SUM" '
  .Path == "github.com/ceyewan/genesis" and
  .Version == $version and
  .Sum == $sum and
  .GoModSum == $go_mod_sum and
  (.Error == null)
' <<<"$download" >/dev/null

jq -n \
  --arg version "$EXPECTED_VERSION" \
  --arg sum "$EXPECTED_SUM" \
  --arg go_mod_sum "$EXPECTED_GO_MOD_SUM" \
  --arg gowork "$GOWORK" \
  --arg resonance_sha "$(git rev-parse HEAD)" \
  '{schema_version:1,module:"github.com/ceyewan/genesis",version:$version,sum:$sum,go_mod_sum:$go_mod_sum,gowork:$gowork,replace:false,local_source_copy:false,resonance_sha:$resonance_sha}'
