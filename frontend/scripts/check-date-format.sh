#!/usr/bin/env bash
# Forbids bypassing the canonical <Timestamp> render path for Date display.
# Q-002 (2026-05-03) replaced the last two bypassing callsites in produce.tsx
# and messages.tsx; this lint prevents regression.
#
# Forbidden pattern: any call to Date#toLocaleString | toLocaleDateString |
# toLocaleTimeString | toString | toISOString | toDateString | toTimeString
# inside frontend/src/**/*.{ts,tsx}.
#
# Whitelisted by file (rendering primitives that internally call these):
#   - src/lib/format.ts
#   - src/components/timestamp.tsx
#   - src/components/relative-time.tsx
#
# Whitelisted line-locally with the marker `// allow-raw-date: <reason>` on
# the same line OR the line immediately above (use for filename stamps,
# JSON-export fields, and other non-display serialization).
#
# This script catches Date#toLocaleString but intentionally NOT
# Number#toLocaleString (the latter is the canonical thousands-separator
# formatter in <DataTable> cells). Detection is purely textual; false
# positives on `someNumber.toLocaleString()` are filtered by the wrapping
# `new Date(...)` heuristic plus `\.toLocale(Date|Time)String` for the
# date-specific variants.
#
# Sister scripts: check-strings.sh, check-tokens.sh, check-palette.sh,
# check-route-orphans.sh.

set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${ROOT}/src"

WHITELIST_FILES=(
  "src/lib/format.ts"
  "src/components/timestamp.tsx"
  "src/components/relative-time.tsx"
)

# Pattern A — Date#toLocale*: ".toLocaleDateString(" / ".toLocaleTimeString("
# These are unambiguously date-specific and never apply to Number.
PATTERN_DATE_LOCALE='\.toLocale(Date|Time)String\('

# Pattern B — bare ".toLocaleString(" on a Date. We catch the common shape
# `new Date(...).toLocaleString` directly; bare `x.toLocaleString` may be
# `Number#toLocaleString` and is not flagged.
PATTERN_DATE_TOLOCALE='new Date\([^)]*\)\.toLocaleString\('

# Pattern C — Date#toString / toISOString / toDateString / toTimeString
# directly chained off `new Date(...)`.
PATTERN_DATE_TOSTRING='new Date\([^)]*\)\.(toString|toISOString|toDateString|toTimeString)\('

cd "${ROOT}/.."

# Build the file list under frontend/src, minus whitelisted files and tests.
FILES=$(find frontend/src -type f \( -name '*.ts' -o -name '*.tsx' \) | grep -v '\.test\.' | sort)

EXISTING=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  rel="${f#frontend/}"
  skip=""
  for w in "${WHITELIST_FILES[@]}"; do
    if [ "$rel" = "$w" ]; then
      skip=1
      break
    fi
  done
  [ -n "$skip" ] && continue
  EXISTING+="$f"$'\n'
done <<< "${FILES}"

# Run the three patterns in one combined alternation, then strip lines that
# carry the allow-raw-date marker on the same line OR have it on the line
# immediately above.
COMBINED="${PATTERN_DATE_LOCALE}|${PATTERN_DATE_TOLOCALE}|${PATTERN_DATE_TOSTRING}"

RAW_HITS=$(printf '%s' "${EXISTING}" | tr '\n' '\0' \
  | xargs -0 grep -EHn -- "${COMBINED}" 2>/dev/null \
  || true)

if [ -z "${RAW_HITS}" ]; then
  exit 0
fi

# Filter: drop hits whose line contains the marker, or whose immediately
# preceding line in the file contains the marker.
FAIL=0
FILTERED=""
while IFS= read -r hit; do
  [ -z "$hit" ] && continue
  file="${hit%%:*}"
  rest="${hit#*:}"
  lineno="${rest%%:*}"
  text="${rest#*:}"
  if grep -q 'allow-raw-date:' <<< "$text"; then
    continue
  fi
  if [ "$lineno" -gt 1 ]; then
    prev=$(sed -n "$((lineno - 1))p" "$file" 2>/dev/null || true)
    if grep -q 'allow-raw-date:' <<< "$prev"; then
      continue
    fi
  fi
  FILTERED+="$hit"$'\n'
  FAIL=1
done <<< "${RAW_HITS}"

if [ "${FAIL}" -eq 1 ]; then
  echo "check-date-format: raw Date formatters bypass <Timestamp> (Q-002):" >&2
  printf '%s' "${FILTERED}" >&2
  echo "" >&2
  echo "Fix: import { Timestamp } from '@/components/timestamp' and use" >&2
  echo "  <Timestamp value={msOrIso} />" >&2
  echo "or whitelist the line with: // allow-raw-date: <reason>" >&2
  exit 1
fi

exit 0
