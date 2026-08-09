#!/usr/bin/env bash
set -euo pipefail

target=${1:-}
if [[ -z "$target" || ! -e "$target" ]]; then
  echo "usage: $0 <evidence-file-or-directory>" >&2
  exit 2
fi

# Match secret-bearing field names/assignments and common credential payloads.
# Report file names only so a failed scan never prints the credential value.
sensitive_pattern='(?i)([a-z0-9_.-]*(password|passwd|secret|token|api[_-]?key|auth[_-]?key|signing[_-]?key|private[_-]?key)[a-z0-9_.-]*"?[[:space:]]*[:=]|authorization"?[[:space:]]*[:=]|-----BEGIN [A-Z ]*PRIVATE KEY-----|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,})'

if matches=$(rg --hidden --no-ignore --files-with-matches --pcre2 \
  "$sensitive_pattern" "$target" 2>/dev/null); then
  echo "refusing evidence: sensitive field or credential pattern detected in:" >&2
  printf '%s\n' "$matches" >&2
  exit 1
fi

echo "evidence sensitive-field scan: PASS"
