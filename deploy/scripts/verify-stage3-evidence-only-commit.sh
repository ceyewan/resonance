#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
tested_sha=${1:-}
candidate=${2:-HEAD}
[[ "$tested_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "usage: $0 TESTED_SHA [CANDIDATE]" >&2; exit 2; }
git cat-file -e "$tested_sha^{commit}"
git cat-file -e "$candidate^{commit}"

manifest=docs/verification/evidence/resonance-stage3-genesis-v1.0.0-rc.2.json
changed=$(git diff --name-only "$tested_sha..$candidate")
[[ -n "$changed" ]] || { echo "evidence commit has no changes" >&2; exit 1; }
while IFS= read -r path; do
  case "$path" in
    "$manifest"|artifacts/local-v1/rc2-adoption-final/*) ;;
    *) echo "evidence-only commit changes runtime/build input: $path" >&2; exit 1 ;;
  esac
done <<<"$changed"

git show "$candidate:$manifest" >"${TMPDIR:-/tmp}/resonance-stage3-manifest.$$"
manifest_tmp="${TMPDIR:-/tmp}/resonance-stage3-manifest.$$"
trap 'rm -f "$manifest_tmp"' EXIT
jq -e --arg tested_sha "$tested_sha" '
  .status == "PASS" and .tested_resonance_sha == $tested_sha and
  (.evidence.locator | test("^artifacts/local-v1/rc2-adoption-final/[0-9]{8}T[0-9]{6}Z$")) and
  (.evidence.bundle_manifest_sha256 | test("^[0-9a-f]{64}$"))
' "$manifest_tmp" >/dev/null
locator=$(jq -er '.evidence.locator' "$manifest_tmp")
bundle="$locator/bundle.sha256"
git cat-file -e "$candidate:$bundle"
expected_bundle_sha=$(jq -er '.evidence.bundle_manifest_sha256' "$manifest_tmp")
actual_bundle_sha=$(git show "$candidate:$bundle" | shasum -a 256 | awk '{print $1}')
[[ "$actual_bundle_sha" == "$expected_bundle_sha" ]] || {
  echo "bundle manifest hash does not match the adoption manifest" >&2
  exit 1
}

while read -r digest relative_path; do
  relative_path=${relative_path#./}
  [[ "$digest" =~ ^[0-9a-f]{64}$ && -n "$relative_path" ]] || {
    echo "invalid bundle entry" >&2; exit 1;
  }
  actual=$(git show "$candidate:$locator/$relative_path" | shasum -a 256 | awk '{print $1}')
  [[ "$actual" == "$digest" ]] || { echo "bundle hash mismatch: $relative_path" >&2; exit 1; }
done < <(git show "$candidate:$bundle")

echo "Stage 3 evidence-only commit: PASS"
