#!/usr/bin/env bash
# PostToolUse hook (Edit|Write) for files under frontend/src. Two groups of
# checks on the one file Claude just changed:
#
#   1. The rules of frontend/src/__checks__/tokens.test.ts: every
#      var(--color-X) must be declared in frontend/src/index.css, and a token
#      is used through its utility, never as an arbitrary [var(--color-X)]
#      class. The test (part of `bun run test` and `make check`) fails
#      otherwise.
#   2. For .tsx files, design-guideline rules that no automated gate catches,
#      each cited from docs/DESIGN_GUIDELINES.md: colour literals (§ 12.1),
#      default Tailwind palette classes, which generate no CSS because the
#      palette is disabled in @theme (§ 2.2, § 12.2), ring-* or border-*
#      focus styles (§ 4.4, § 9) and outline-none (§ 6.1, § 9).
#
# Findings go to stderr with exit 2 so Claude sees them; the edit stays.
#
# Test: echo '{"tool_input":{"file_path":"/repo/frontend/src/routes/index.tsx"}}' | .claude/hooks/design-token-guard.sh; echo $?
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
  frontend/src/index.css) exit 0 ;;
  frontend/src/*.ts|frontend/src/*.tsx|frontend/src/*.css) ;;
  *) exit 0 ;;
esac

findings=()

# 1. Mirror of src/__checks__/tokens.test.ts, limited to this file.
css="$root/frontend/src/index.css"
if [ -f "$css" ]; then
  declared=$(sed -nE 's/^[[:space:]]*(--color-[a-z0-9-]+)[[:space:]]*:.*/\1/p' "$css")
  while IFS=: read -r line match; do
    [ -n "$match" ] || continue
    token=${match#var(}
    if ! grep -qxF -- "$token" <<<"$declared"; then
      findings+=("$rel:$line: var($token) is not declared in frontend/src/index.css; src/__checks__/tokens.test.ts fails on it")
    fi
  done < <(grep -onE 'var\(--color-[a-z0-9-]+' "$file" || true)
  # Second rule of the same test: a token is used through its utility
  # (bg-panel), never as an arbitrary value ([var(--color-panel)]).
  while IFS=: read -r line match; do
    [ -n "$match" ] || continue
    findings+=("$rel:$line: $match uses an arbitrary var(--color-*) value; use the token utility (bg-panel, text-muted); src/__checks__/tokens.test.ts fails on it")
  done < <(grep -onE '[^[:space:]]*\[var\(--color-[a-z0-9-]+\)\][^[:space:]]*' "$file" || true)
fi

# 2. Guideline rules for component code. Comment lines are skipped; $3 is an
#    optional regex for lines to leave alone.
scan() {
  local regex=$1 message=$2 skip=${3:-}
  while IFS=: read -r line text; do
    [ -n "$line" ] || continue
    [[ $text =~ ^[[:space:]]*(//|\*|/\*) ]] && continue
    if [ -n "$skip" ] && [[ $text =~ $skip ]]; then continue; fi
    findings+=("$rel:$line: $message")
  done < <(grep -nE -- "$regex" "$file" || true)
}

case "$rel" in
  *.tsx)
    # Inline SVG paint attributes (the logo in Shell.tsx) are the known
    # exception for colour literals.
    scan '#([0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[a-fA-F][0-9a-fA-F]{2}|[0-9][a-fA-F][0-9a-fA-F]|[0-9]{2}[a-fA-F])\b|\b(rgba?|hsla?|oklch)\(' \
      'colour literal in component code; use a token utility such as bg-panel or text-muted (DESIGN_GUIDELINES § 2.2, § 12.1)' \
      '(stopColor|fill|stroke)="#'
    scan '\b(bg|text|border|ring|fill|stroke|from|to|via|outline|divide|placeholder|accent|caret|decoration|shadow)-(slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|white|black)(-[0-9]{2,3})?\b' \
      'default Tailwind palette class; the palette is disabled in @theme so it generates no CSS. Use the tokens from frontend/src/index.css (DESIGN_GUIDELINES § 2.2, § 12.2)'
    scan '\bfocus(-visible|-within)?:(ring\b|ring-|border-)' \
      'custom focus style; the global :focus-visible outline in index.css is the focus indicator, ring-* is clipped by overflow:hidden (DESIGN_GUIDELINES § 4.4, § 9)'
    scan '\boutline-none\b' \
      'outline-none removes the global focus indicator (DESIGN_GUIDELINES § 6.1, § 9)'
    ;;
esac

if [ "${#findings[@]}" -gt 0 ]; then
  printf 'Design guard: %d finding(s) in %s (the edit stays; fix them before bun run lint / make check):\n' "${#findings[@]}" "$rel" >&2
  printf '  %s\n' "${findings[@]}" >&2
  exit 2
fi

exit 0
