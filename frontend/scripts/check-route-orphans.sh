#!/usr/bin/env bash
# Fails if any flat-named child route file under frontend/src/routes/ has no
# parent layout file. TanStack file-based routing is permissive (the build
# does not error on orphans, see Q-005 and Context7 research 2026-05-03), so
# this static check is the cheap fast-fail counterpart to the route-tree
# Vitest smoke test (frontend/src/routes/route-tree.test.ts).
#
# Rule: for any file `<segments>.tsx` with two or more dot-separated segments,
# the parent layout `<segments-minus-last>.tsx` must exist.
#
#   clusters.$cluster.tsx           → parent clusters.tsx
#   clusters.$cluster.security.tsx  → parent clusters.$cluster.tsx
#   settings.clusters.tsx           → parent settings.tsx
#   index.tsx                       → no parent (root index)
#   __root.tsx                      → skipped (root layout)
#
# Sister scripts: check-strings.sh, check-tokens.sh, check-palette.sh.

set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ROUTES_DIR="${ROOT}/src/routes"

if [ ! -d "${ROUTES_DIR}" ]; then
  echo "check-route-orphans: ${ROUTES_DIR} not found" >&2
  exit 1
fi

cd "${ROUTES_DIR}"

orphans=()
for f in *.tsx; do
  [ -e "$f" ] || continue
  [[ "$f" == __* ]] && continue
  stem="${f%.tsx}"
  case "$stem" in
    *.*) ;;
    *) continue ;;
  esac
  parent="${stem%.*}.tsx"
  [ -f "$parent" ] || orphans+=("$f → missing parent $parent")
done

if [ ${#orphans[@]} -ne 0 ]; then
  echo "check-route-orphans: orphaned routes (no parent layout):" >&2
  printf '  %s\n' "${orphans[@]}" >&2
  echo "TanStack does not error on these; child renders under __root.tsx," >&2
  echo "which usually breaks the visual layout chain. See specs Q-005." >&2
  exit 1
fi

exit 0
