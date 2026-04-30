#!/usr/bin/env bash
# Fails if any Direction-A-banned Tailwind palette utility (bg-slate-*,
# text-gray-*, border-zinc-*, …) appears in *added* lines of the current
# diff vs the base branch. Legacy files that still contain them are
# intentionally ignored until they are migrated.
#
# See: docs/DESIGN_GUIDELINES.md § 2. Use @theme tokens instead
# (bg-panel, text-muted, border-border, text-tint-red-fg, …).
#
# Runs locally; mirrors .github/workflows/design-guardrails.yml which
# fires on PRs once GitHub Actions is re-enabled.
#
# Usage:
#   frontend/scripts/check-palette.sh                # diff vs origin/main
#   BASE=origin/develop frontend/scripts/check-palette.sh
#   frontend/scripts/check-palette.sh --working-tree # scan files with uncommitted
#                                                    # changes (pre-commit, stop-hook)
#   frontend/scripts/check-palette.sh --all          # scan every file
#     (useful once legacy has been migrated)

set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PATTERN='\b(bg|text|border|ring|from|to|via|fill|stroke|divide)-(slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-[0-9]+'
BASE="${BASE:-origin/main}"

cd "${ROOT}/.."

if [ "${1:-}" = "--all" ]; then
  HITS=$(grep -rEn --include='*.ts' --include='*.tsx' --include='*.css' "${PATTERN}" frontend/src || true)
  SCOPE="all files under frontend/src"
elif [ "${1:-}" = "--working-tree" ]; then
  # Files with any local changes (staged, unstaged, or untracked) under frontend/.
  # `ls-files -m -o --exclude-standard` covers modified + untracked (not gitignored).
  FILES=$({ git diff --name-only --cached -- frontend; \
            git ls-files -m -o --exclude-standard -- frontend; } \
    | sort -u \
    | grep -E '\.(ts|tsx|css)$' || true)
  if [ -z "${FILES}" ]; then
    exit 0
  fi
  # shellcheck disable=SC2086
  HITS=$(printf '%s\n' "${FILES}" | tr '\n' '\0' | xargs -0 grep -EHn "${PATTERN}" 2>/dev/null || true)
  SCOPE="files with uncommitted changes"
else
  if ! git rev-parse --verify --quiet "${BASE}" > /dev/null; then
    echo "check-palette: base ref '${BASE}' not found; skipping diff check" >&2
    exit 0
  fi
  # Added lines only (leading '+' in unified diff, excluding the '+++' header).
  HITS=$(git diff "${BASE}...HEAD" -- 'frontend/**/*.ts' 'frontend/**/*.tsx' 'frontend/**/*.css' \
    | grep -E "^\+[^+].*${PATTERN}" || true)
  SCOPE="diff ${BASE}...HEAD"
fi

if [ -n "${HITS}" ]; then
  echo "check-palette: default Tailwind palette classes in ${SCOPE} (use @theme tokens — see docs/DESIGN_GUIDELINES.md §2):" >&2
  echo "${HITS}" >&2
  exit 1
fi
