#!/usr/bin/env bash
# English-only linter for kafkito frontend UI strings (CLAUDE.md "UI strings
# and code comments are English only" rule).
#
# Two-stage scan over `frontend/src/**/*.{ts,tsx}`:
#
#   Stage 1 — known-German-token blocklist. The wordlist is the canonical set
#             from PLAN.md § 1.1 plus the residuals surfaced by the
#             1.1-followup commit (557c3c2). Case-insensitive, word-boundary
#             anchored. Any hit fails the lint.
#
#   Stage 2 — German-letter regex `[äöüßÄÖÜ]`. Catches German words the
#             wordlist missed without false-positiving on the legitimate
#             Unicode glyphs used elsewhere in the codebase
#             (· … — › – ± − ≠ ≤ ≥ ∞ × ÷ → ↓ ↑ ↵ ↔ ▲ ▼ ▸ ▾ ⌘ § • ⚠ ✕ ⌕,
#             plus curly quotes), per Q-003 lessons-learned. ß is German-only;
#             umlauts are German-or-loanword — accept the negligible
#             false-positive risk and log to OPEN_QUESTIONS.md if it bites.
#
# Same triple-mode CLI as `check-palette.sh`:
#   frontend/scripts/check-strings.sh                # diff vs origin/main
#   BASE=origin/develop frontend/scripts/check-strings.sh
#   frontend/scripts/check-strings.sh --working-tree # files with uncommitted
#                                                    # changes (pre-commit, stop-hook)
#   frontend/scripts/check-strings.sh --all          # scan every file
#
# Sister scripts: check-palette.sh (banned Tailwind palette utilities),
# check-tokens.sh (verifies var(--color-X) tokens exist).

set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="${BASE:-origin/main}"

# Stage 1 — canonical German-token wordlist (PLAN § 1.1 + 1.1-followup).
# Word-boundary anchored via `\b` in the alternation. `grep -i` for case
# insensitivity. Update this list when a new German cognate slips through.
WORDLIST='\b(Nachricht|Suchen|Letzte|Lade|Aktualisieren|Veroeffentlichen|Veröffentlichen|Bestaetigen|Bestätigen|Abbrechen|Loeschen|Löschen|Erstellen|Heute|Gestern|Tipp:|Klicke|Pfad|Beispiel|Zeitraum|Benutzerdefiniert|Verstanden|Uebernehmen|Übernehmen|Rueckgaengig|Rückgängig|Tippen|Felder|Eintraege|Einträge|Allen|Parse-Fehler|uebersprungen|übersprungen|erschoepft|erschöpft|wurde|durch|veraendert|verändert|loeschen|löschen)\b'

# Stage 2 — German letters (umlauts + ß, both cases). Fires on any line
# containing one of these characters. Distinct exit message from Stage 1
# so the failure mode is obvious in CI logs.
LETTER_PATTERN='[äöüßÄÖÜ]'

cd "${ROOT}/.."

# Build the file list per the chosen mode, exit 0 if no files in scope.
if [ "${1:-}" = "--all" ]; then
  FILES=$(find frontend/src -type f \( -name '*.ts' -o -name '*.tsx' \) | sort)
  SCOPE="all .ts/.tsx files under frontend/src"
elif [ "${1:-}" = "--working-tree" ]; then
  FILES=$({ git diff --name-only --cached -- frontend; \
            git ls-files -m -o --exclude-standard -- frontend; } \
    | sort -u \
    | grep -E '\.(ts|tsx)$' || true)
  if [ -z "${FILES}" ]; then
    exit 0
  fi
  SCOPE="files with uncommitted changes"
else
  if ! git rev-parse --verify --quiet "${BASE}" > /dev/null; then
    echo "check-strings: base ref '${BASE}' not found; skipping diff check" >&2
    exit 0
  fi
  FILES=$(git diff --name-only "${BASE}...HEAD" -- 'frontend/**/*.ts' 'frontend/**/*.tsx' \
    | sort -u || true)
  if [ -z "${FILES}" ]; then
    exit 0
  fi
  SCOPE="diff ${BASE}...HEAD"
fi

# Filter the file list down to files that still exist (handles renames /
# deletions in --diff and --working-tree modes).
EXISTING=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  [ -f "$f" ] && EXISTING+="$f"$'\n'
done <<< "${FILES}"
if [ -z "${EXISTING}" ]; then
  exit 0
fi

FAIL=0

# Stage 1 — wordlist hits (case-insensitive, word-boundary).
HITS_WORDS=$(printf '%s' "${EXISTING}" | tr '\n' '\0' | xargs -0 grep -EHniw "${WORDLIST}" 2>/dev/null || true)
if [ -n "${HITS_WORDS}" ]; then
  echo "check-strings: German UI tokens in ${SCOPE} (CLAUDE.md 'English only' rule):" >&2
  echo "${HITS_WORDS}" >&2
  FAIL=1
fi

# Stage 2 — German letters [äöüßÄÖÜ].
HITS_LETTERS=$(printf '%s' "${EXISTING}" | tr '\n' '\0' | xargs -0 grep -EHn "${LETTER_PATTERN}" 2>/dev/null || true)
if [ -n "${HITS_LETTERS}" ]; then
  echo "check-strings: German letters [äöüßÄÖÜ] in ${SCOPE} (CLAUDE.md 'English only' rule):" >&2
  echo "${HITS_LETTERS}" >&2
  FAIL=1
fi

exit "${FAIL}"
