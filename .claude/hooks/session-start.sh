#!/usr/bin/env bash
# SessionStart hook for Claude Code cloud sessions (CLAUDE_CODE_REMOTE=true):
# installs what `make check` and the formatting hooks need, so tests and
# linters run in a fresh container. Local sessions are left alone. Everything
# is idempotent; a step that cannot run is reported, not fatal.
#
# Test: echo '{"source":"startup"}' | CLAUDE_CODE_REMOTE=true .claude/hooks/session-start.sh; echo $?
set -euo pipefail

[ "${CLAUDE_CODE_REMOTE:-}" = "true" ] || exit 0

input=$(cat 2>/dev/null || true)
# Compaction keeps the container; nothing to install.
case "$input" in
  *'"source"'*'"compact"'*) exit 0 ;;
esac

root="${CLAUDE_PROJECT_DIR:-$PWD}"
cd "$root"

ok=()
notes=()

# 1. Frontend dependencies (Biome, TypeScript, knip, Vitest, Playwright).
if command -v bun >/dev/null 2>&1; then
  if (cd frontend && bun install --frozen-lockfile >/dev/null 2>&1); then
    ok+=("frontend dependencies (bun $(bun --version))")
  else
    notes+=("bun install --frozen-lockfile failed in frontend/; run it to see why")
  fi
else
  notes+=("bun is not installed: frontend checks (bun run lint/test/build) are unavailable")
fi

# 2. Go modules, so the first `go test` does not wait on downloads.
if command -v go >/dev/null 2>&1; then
  if go mod download >/dev/null 2>&1; then
    ok+=("Go modules ($(go version | cut -d' ' -f3))")
  else
    notes+=("go mod download failed; go test will fetch modules on demand")
  fi
else
  notes+=("go is not installed: make test/lint/build are unavailable")
fi

# 3. The pinned golangci-lint in ./bin (GOLANGCI_LINT_VERSION in the Makefile).
#    `make lint-install` runs the upstream install script, whose tag lookup on
#    github.com is refused by some egress policies. The fallback fetches the
#    same release archive and checksum file from the same release and verifies
#    the SHA-256 itself, like the script does.
install_golangci_lint_fallback() {
  local version ver os arch tarball base tmp
  version=$(sed -n 's/^GOLANGCI_LINT_VERSION := //p' Makefile)
  [ -n "$version" ] || return 1
  ver=${version#v}
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
  esac
  tarball="golangci-lint-${ver}-${os}-${arch}.tar.gz"
  base="https://github.com/golangci/golangci-lint/releases/download/${version}"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  curl -sSfL -o "$tmp/$tarball" "$base/$tarball" || return 1
  curl -sSfL -o "$tmp/checksums.txt" "$base/golangci-lint-${ver}-checksums.txt" || return 1
  (cd "$tmp" && grep " ${tarball}\$" checksums.txt | sha256sum -c --quiet) || return 1
  tar -xzf "$tmp/$tarball" -C "$tmp" || return 1
  mkdir -p bin
  install -m 0755 "$tmp/golangci-lint-${ver}-${os}-${arch}/golangci-lint" bin/golangci-lint
}

if command -v go >/dev/null 2>&1 && command -v curl >/dev/null 2>&1; then
  if make -s lint-install >/dev/null 2>&1 || install_golangci_lint_fallback; then
    ok+=("golangci-lint $(bin/golangci-lint version --short 2>/dev/null || echo '?') in ./bin")
  else
    notes+=("golangci-lint could not be installed: make lint and the Go formatting hook are unavailable")
  fi
fi

# 4. Docker: needed by make dev, make e2e and make test-integration.
if ! docker info >/dev/null 2>&1; then
  notes+=("Docker is not available: make dev, make e2e and make test-integration cannot run in this session")
fi

# stdout becomes context for Claude.
echo "kafkito cloud session setup (.claude/hooks/session-start.sh):"
for line in ${ok[@]+"${ok[@]}"}; do echo "  ready: $line"; done
for line in ${notes[@]+"${notes[@]}"}; do echo "  note: $line"; done
exit 0
