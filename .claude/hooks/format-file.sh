#!/usr/bin/env bash
# PostToolUse hook (Edit|Write): formats the one file Claude just changed with
# the repository's pinned formatters, the same ones the pre-commit hooks and
# `make lint` / `make frontend-check` use:
#   - Go:      bin/golangci-lint fmt <file>   (gofmt + goimports, .golangci.yaml)
#   - TS/TSX:  frontend/node_modules/.bin/biome check --write <file>
# Generated files are skipped. A missing tool is not an error (run
# `make lint-install` / `make frontend-install`; the SessionStart hook does so
# in cloud sessions). Diagnostics the formatter cannot fix are reported back
# to Claude with exit 2; the edit itself stays.
#
# Test: echo '{"tool_input":{"file_path":"/repo/internal/server/topics.go"}}' | .claude/hooks/format-file.sh; echo $?
set -euo pipefail

input=$(cat)
root="${CLAUDE_PROJECT_DIR:-$PWD}"

hook_field() {
  if command -v jq >/dev/null 2>&1; then
    jq -r "$1 // empty" <<<"$input"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -I -c '
import json, sys
value = json.load(sys.stdin)
for key in sys.argv[1].strip(".").split("."):
    value = value.get(key, "") if isinstance(value, dict) else ""
print(value if isinstance(value, str) else "")' "$1" <<<"$input"
  fi
}

file=$(hook_field .tool_input.file_path)
[ -n "$file" ] && [ -f "$file" ] || exit 0
rel="${file#"$root"/}"

case "$rel" in
  *.gen.go|frontend/src/lib/api.gen.ts|*/routeTree.gen.ts)
    exit 0 ;;
  *.go)
    lint="$root/bin/golangci-lint"
    [ -x "$lint" ] || exit 0
    cd "$root"
    if ! out=$("$lint" fmt "$rel" 2>&1); then
      printf 'golangci-lint fmt failed on %s:\n%s\n' "$rel" "$(printf '%s\n' "$out" | head -n 40)" >&2
      exit 2
    fi
    ;;
  frontend/*.ts|frontend/*.tsx)
    biome="$root/frontend/node_modules/.bin/biome"
    [ -x "$biome" ] || exit 0
    cd "$root/frontend"
    # Same flags as the biome-check pre-commit hook; --write applies the
    # formatter and the safe lint fixes, everything else is reported.
    if ! out=$("$biome" check --write --no-errors-on-unmatched --files-ignore-unknown=true "${rel#frontend/}" 2>&1); then
      printf 'biome check --write left diagnostics in %s (bun run lint fails on them):\n%s\n' "$rel" "$(printf '%s\n' "$out" | head -n 40)" >&2
      exit 2
    fi
    ;;
esac

exit 0
