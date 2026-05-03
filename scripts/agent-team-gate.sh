#!/usr/bin/env bash
# agent-team-gate.sh — TaskCompleted hook for the kafkito TDD agent team.
#
# Detects which gate(s) to run based on files changed since the last
# commit (or the merge-base with main, whichever is closer). Runs every
# gate that applies to the changed files, in order.
#
# Exit code 0  = all gates green; task may complete.
# Exit code 2  = at least one gate failed; task completion is blocked.
#                stderr is fed back to the teammate as feedback.
# Other exit   = non-blocking notice; task still completes.
#
# Hook contract (Claude Code TaskCompleted):
#   - JSON payload on stdin: {session_id, transcript_path, cwd,
#     hook_event_name, task_id, task_name, task_status}. We ignore it.
#   - Exit 2 + stderr is the channel that talks to the teammate.

set -uo pipefail

repo_root="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
cd "$repo_root" || { echo "agent-team-gate: cannot cd to $repo_root" >&2; exit 2; }

# Serialize concurrent hook invocations across teammates. Without this, two
# overlapping gates race on shared resources:
#   - golangci-lint's on-disk cache lock fails fast with "parallel
#     golangci-lint is running";
#   - `bun run build` (invoked by `make e2e-up`) rewrites frontend/dist/
#     while a sibling `go test` is compiling the embed.FS, so internal/server's
#     SPA-fallback tests transiently 500 because index.html is briefly
#     missing from the embedded snapshot.
# Both are eliminated by running gate invocations one at a time. Portable
# mkdir-lock (atomic on POSIX) with a trap-released lockdir.
LOCKDIR="${TMPDIR:-/tmp}/kafkito-agent-team-gate.lock"
LOCK_TIMEOUT_SECONDS=900
lock_elapsed=0
while ! mkdir "$LOCKDIR" 2>/dev/null; do
  # Self-heal stale locks: if the owner PID is no longer running, clear
  # and retry. This protects against bash invocations whose trap failed
  # (e.g., SIGKILL or older script versions whose trap used rmdir without
  # first removing the owner file).
  if [ -f "$LOCKDIR/owner" ]; then
    owner_pid="$(awk '{print $1}' "$LOCKDIR/owner" 2>/dev/null || true)"
    if [ -n "$owner_pid" ] && ! kill -0 "$owner_pid" 2>/dev/null; then
      echo "agent-team-gate: clearing stale lock from dead pid $owner_pid" >&2
      rm -f "$LOCKDIR/owner" 2>/dev/null
      rmdir "$LOCKDIR" 2>/dev/null
      continue
    fi
  fi
  if [ "$lock_elapsed" -ge "$LOCK_TIMEOUT_SECONDS" ]; then
    echo "agent-team-gate: lock timeout after ${LOCK_TIMEOUT_SECONDS}s — another gate may be stuck. Inspect $LOCKDIR/owner and remove the directory if stale." >&2
    exit 2
  fi
  sleep 3
  lock_elapsed=$((lock_elapsed+3))
done
echo "$$ pid=$$ started=$(date -u +%FT%TZ)" > "$LOCKDIR/owner" 2>/dev/null || true
# rm -rf is required (not rmdir): the lockdir contains the owner file, and
# rmdir refuses non-empty dirs — that bug let stale locks survive across
# completed hook runs. Belt-and-suspenders: trap on every exit signal we can.
trap 'rm -rf "$LOCKDIR" 2>/dev/null || true' EXIT INT TERM HUP

# Determine the diff base. Prefer HEAD~1 if it exists; fall back to
# merge-base with origin/main; fall back to working-tree changes.
if git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
  base="HEAD~1"
elif git rev-parse --verify origin/main >/dev/null 2>&1; then
  base="$(git merge-base HEAD origin/main 2>/dev/null || echo HEAD)"
else
  base="HEAD"
fi

changed="$(
  {
    git diff --name-only "$base" HEAD 2>/dev/null
    git diff --name-only --cached 2>/dev/null
    git diff --name-only 2>/dev/null
  } | sort -u
)"

if [ -z "$changed" ]; then
  echo "agent-team-gate: no changes detected; nothing to verify."
  exit 0
fi

needs_backend=0
needs_frontend_unit=0
needs_e2e=0

while IFS= read -r f; do
  case "$f" in
    pkg/*|internal/*|cmd/*|proto/*|go.mod|go.sum) needs_backend=1 ;;
    frontend/e2e/*|frontend/playwright.config.ts|docker-compose*.yml) needs_e2e=1 ;;
    frontend/*) needs_frontend_unit=1 ;;
  esac
done <<< "$changed"

failures=()
log_buf=""

run_gate() {
  local label="$1"
  shift
  log_buf+="
=== gate: $label ===
"
  if ! out="$("$@" 2>&1)"; then
    failures+=("$label")
    log_buf+="$out"$'\n'
  else
    # Keep the tail so the teammate can see what passed.
    log_buf+="$(printf '%s\n' "$out" | tail -n 5)"$'\n'
  fi
}

if [ "$needs_backend" -eq 1 ]; then
  run_gate "backend: go test -race"  bash -c 'go test ./... -race'
  run_gate "backend: golangci-lint"  bash -c 'golangci-lint run'
fi

if [ "$needs_frontend_unit" -eq 1 ]; then
  run_gate "frontend: lint"          bash -c 'cd frontend && bun run lint'
  run_gate "frontend: build"         bash -c 'cd frontend && bun run build'
  run_gate "frontend: check:palette" bash -c 'cd frontend && bun run check:palette'
  run_gate "frontend: check:strings" bash -c 'cd frontend && bun run check:strings'
  run_gate "frontend: check:tokens"  bash -c 'cd frontend && bun run check:tokens'
  run_gate "frontend: check:routes"  bash -c 'cd frontend && bun run check:routes'
  run_gate "frontend: check:dates"   bash -c 'cd frontend && bun run check:dates'
  run_gate "frontend: vitest"        bash -c 'cd frontend && bun run test'
fi

if [ "$needs_e2e" -eq 1 ]; then
  # `make e2e` runs e2e-up (docker compose up kafka, frontend build, kafkito-e2e
  # binary on KAFKITO_E2E_BASE_URL, seed) → e2e-test (playwright) → e2e-down.
  # This is the canonical hermetic flow from commit cebaf1f. Calling
  # `bun run e2e` directly was wrong on two counts: no such npm script exists,
  # and Playwright needs the kafkito-e2e binary running, not just Kafka.
  run_gate "e2e: make e2e (hermetic)" bash -c 'make e2e'
fi

if [ "${#failures[@]}" -gt 0 ]; then
  {
    echo "agent-team-gate: BLOCKED — ${#failures[@]} gate(s) failed:"
    for f in "${failures[@]}"; do
      echo "  - $f"
    done
    echo
    echo "Detailed log:"
    echo "$log_buf"
    echo
    echo "Re-run locally, fix, then resubmit. The TaskCompleted hook"
    echo "will re-evaluate on the next attempt."
  } >&2
  exit 2
fi

echo "agent-team-gate: all applicable gates green."
exit 0
