---
name: ui-change
description: Implement a new page or any visible change under frontend/ against the kafkito design system: DESIGN_GUIDELINES § 11 checklist, the five view states in light and dark, the accessibility gates, a § 14 changelog row for token or primitive changes, and an e2e walk where needed. Use for new routes, components, primitives, tokens, copy or layout changes in frontend/src.
---

# Build or change UI

Read `frontend/AGENTS.md` first, then the closest existing route under
`frontend/src/routes/`. Canonical sources: `frontend/src/index.css`,
`docs/DESIGN_GUIDELINES.md` (§ 13 on conflicts). The design-system artifact
linked in `frontend/AGENTS.md` is an optional reference.

1. **Place it.** Generic and Kafka-free: `src/components/ui/<Name>.tsx`.
   Bound to one domain: `src/features/<domain>/`. Otherwise keep it in the
   route file until it is reused or needs its own test. PascalCase files.
2. **Compose from the primitives** in `src/components/ui/` (§ 6.1 lists
   them). A missing pattern becomes a primitive first, with a
   `<Name>.test.tsx` and a § 6.1 row; no inlined table chrome, modal
   scaffolding or button styles.
3. **Data.** A `queryOptions()` factory in `src/lib/queries/`, invalidation
   on mutation, `—` plus `// TODO(backend): …` for data the API lacks.
4. **Five states**, each rendered and checked in light and dark: loading
   (skeleton rows or cards), empty (`EmptyState` with icon, reason and
   action), error (`ErrorState` with retry), degraded (amber `Notice` naming
   the missing permission and the fix; a one-time toast via
   `claimOncePerSession` when only one column is affected), populated.
5. **Accessibility.** `aria-label` on every icon-only button, `useFieldError`
   for form errors, no status by colour alone (`StatusDot`, `StatusIcon`,
   `StateBadge`, `LagBadge`), one `h1`, `aria-sort` on sortable headers,
   disabled controls explain why. `bun run lint` runs the Biome a11y rules;
   `e2e/a11y.spec.ts` runs axe in both themes (needs Docker).
6. **Tokens or primitives changed?** Define tokens in both modes in
   `src/index.css`, document in § 2.1 / § 6.1, add a contrast pair to
   `src/__checks__/contrast.test.ts` for a new fg/bg combination, add a
   § 14 changelog row, and write in the PR that the design-system artifact
   needs a re-sync.
7. **e2e.** A new route or a changed destructive flow (reset offsets, delete
   records, ACL or SCRAM mutations) gets or updates an `e2e/<route>.spec.ts`
   walk per `frontend/e2e/README.md` (abort-safe, one route per spec). Run
   `make e2e` when Docker is available; otherwise say in the PR that
   `e2e.yml` covers it.
8. **Check.** `cd frontend && bun run lint && bun run knip && bun run build && bun run test`
   (or `make frontend-check`), then the `design-reviewer` subagent on the diff.
9. **PR body.** Paste the § 11 checklist from `docs/DESIGN_GUIDELINES.md`
   with each box ticked, every `TODO(backend):` added, and every deviation
   from the guidelines with its reason.
