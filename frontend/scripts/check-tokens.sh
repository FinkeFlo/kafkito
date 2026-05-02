#!/usr/bin/env bash
# Token-doesn't-exist linter for kafkito frontend.
#
# Verifies that every `var(--color-X)` reference in TSX/TS/CSS sources points
# to a `--color-X` token actually declared in `frontend/src/index.css`. Catches
# the bug class fixed in PLAN § 1.3b (`var(--color-on-accent)` typo —
# the real token is `--color-accent-foreground`).
#
# Algorithm:
#
#   1. Parse `frontend/src/index.css` for declared `--color-*` tokens. The
#      union of the `@theme { ... }` (light) and `html.dark { ... }` (dark)
#      blocks counts as defined — a token declared in either block is
#      reachable per CSS cascade rules.
#
#   2. Scan target files for `var(--color-X)` references (the form Tailwind v4
#      arbitrary utilities expand to and the form hand-rolled inline styles
#      use). Capture every X.
#
#   3. Any referenced X not in the declared set → fail with location.
#
# Scope is `--color-*` only — other custom-property families (`--font-*`,
# etc.) are out of scope for v1; the bug class we're guarding (PLAN § 1.3b)
# is colour tokens. Extend the regex if needed later.
#
# Same triple-mode CLI as `check-palette.sh`:
#   frontend/scripts/check-tokens.sh                # diff vs origin/main
#   BASE=origin/develop frontend/scripts/check-tokens.sh
#   frontend/scripts/check-tokens.sh --working-tree # files with uncommitted
#                                                   # changes (pre-commit, stop-hook)
#   frontend/scripts/check-tokens.sh --all          # scan every file
#
# Sister scripts: check-palette.sh (banned Tailwind palette utilities),
# check-strings.sh (English-only / German-letter guard).

set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INDEX_CSS="${ROOT}/src/index.css"
BASE="${BASE:-origin/main}"

if [ ! -f "${INDEX_CSS}" ]; then
  echo "check-tokens: ${INDEX_CSS} not found" >&2
  exit 2
fi

# 1. Declared tokens — union of `@theme { ... }` and `html.dark { ... }`.
#    Match lines like `  --color-foo-bar:  oklch(...);` anywhere in the file.
#    `grep -E` extracts the token name; `sort -u` dedupes across the two
#    blocks. The pattern is intentionally generous about whitespace and
#    intentionally restrictive about token-character class to avoid grabbing
#    `var(--color-...)` from the right-hand side.
DECLARED=$(grep -Eo '^[[:space:]]*--color-[a-z0-9-]+[[:space:]]*:' "${INDEX_CSS}" \
  | sed -E 's/^[[:space:]]*(--color-[a-z0-9-]+)[[:space:]]*:.*/\1/' \
  | sort -u)

if [ -z "${DECLARED}" ]; then
  echo "check-tokens: no --color-* tokens parsed from ${INDEX_CSS}" >&2
  exit 2
fi

cd "${ROOT}/.."

# 2. Build the file list per the chosen mode.
if [ "${1:-}" = "--all" ]; then
  FILES=$(find frontend/src -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.css' \) | sort)
  SCOPE="all .ts/.tsx/.css files under frontend/src"
elif [ "${1:-}" = "--working-tree" ]; then
  FILES=$({ git diff --name-only --cached -- frontend; \
            git ls-files -m -o --exclude-standard -- frontend; } \
    | sort -u \
    | grep -E '\.(ts|tsx|css)$' || true)
  if [ -z "${FILES}" ]; then
    exit 0
  fi
  SCOPE="files with uncommitted changes"
else
  if ! git rev-parse --verify --quiet "${BASE}" > /dev/null; then
    echo "check-tokens: base ref '${BASE}' not found; skipping diff check" >&2
    exit 0
  fi
  FILES=$(git diff --name-only "${BASE}...HEAD" -- 'frontend/**/*.ts' 'frontend/**/*.tsx' 'frontend/**/*.css' \
    | sort -u || true)
  if [ -z "${FILES}" ]; then
    exit 0
  fi
  SCOPE="diff ${BASE}...HEAD"
fi

# Filter the file list down to files that still exist (handles renames /
# deletions in --diff and --working-tree modes). Also exclude index.css
# itself — its own `var(--color-X)` aliases are by-construction in the
# declared set since they reference siblings in the same block.
EXISTING=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  [ -f "$f" ] || continue
  case "$f" in
    frontend/src/index.css) ;;
    *) EXISTING+="$f"$'\n' ;;
  esac
done <<< "${FILES}"
if [ -z "${EXISTING}" ]; then
  exit 0
fi

# 3. Extract every `var(--color-X)` reference with its location, where X
#    stops at `,` (fallback form) or `)` (terminator). `grep -EHno` returns
#    `file:line:match` which we keep as the failure context.
REFS=$(printf '%s' "${EXISTING}" | tr '\n' '\0' \
  | xargs -0 grep -EHno 'var\(--color-[a-z0-9-]+' 2>/dev/null \
  | sed -E 's/var\(--color-/--color-/' || true)

if [ -z "${REFS}" ]; then
  exit 0
fi

# 4. Diff referenced vs declared. Any token name in the references that is
#    NOT in the declared set is a failure. We report each failing reference
#    with its `file:line` so the developer can jump straight to it.
DECLARED_FILE=$(mktemp)
printf '%s\n' "${DECLARED}" > "${DECLARED_FILE}"
trap 'rm -f "${DECLARED_FILE}"' EXIT

UNKNOWN=$(printf '%s\n' "${REFS}" | awk -F: -v decl="${DECLARED_FILE}" '
  BEGIN {
    while ((getline t < decl) > 0) defined[t] = 1
    close(decl)
  }
  {
    file = $1
    line = $2
    token = $3
    if (!(token in defined)) {
      print file ":" line ": var(" token ") — token not declared in frontend/src/index.css"
    }
  }
')

if [ -n "${UNKNOWN}" ]; then
  echo "check-tokens: unknown --color-* tokens in ${SCOPE} (declare them in src/index.css under @theme + html.dark, or fix the typo):" >&2
  echo "${UNKNOWN}" >&2
  exit 1
fi
