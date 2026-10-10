# frontend/AGENTS.md

Rules for `frontend/`. Sources, in order of precedence: `src/index.css`
(`@theme` tokens) and the code; `docs/DESIGN_GUIDELINES.md` (§ 13 resolves
conflicts, § 14 logs every change); then the design-system artifact described
at the end. Before writing a route, read the closest one under `src/routes/`.

## Commands (in `frontend/`)

- `bun run lint` (Biome lint and format check, `tsr generate`, `tsc -b --noEmit`),
  `bun run knip`, `bun run build`, `bun run test` (Vitest, including the
  repo-wide checks in `src/__checks__/`). One file: `bun run test src/lib/format.test.ts`.
- `make frontend-check` from the repo root runs all four, as CI does.
  `make e2e` (Docker) runs the Playwright walks and axe scans in `e2e/`.
- `src/routeTree.gen.ts` and `src/lib/api.gen.ts` are generated (root AGENTS.md).

## Layout and naming

- Domain-free primitives in `src/components/ui/`, domain components in
  `src/features/<domain>/` (`clusters`, `groups`, `messages`, `topics`), the
  app chrome in `src/features/shell/`, hooks and helpers in `src/lib/`,
  routes in `src/routes/` (file-based; cluster-scoped pages are
  `clusters.$cluster.<page>.tsx` under the `clusters.$cluster.tsx` layout).
- Every `.tsx` under `components/`, `features/` and `auth/` is PascalCase,
  tests included (`ConfirmDialog.test.tsx`); everything else is kebab-case.
  Biome enforces both. No `styles/`, `hooks/`, `types/` or `assets/` folders.
- Tests sit next to their component. e2e walks are `e2e/<route>.spec.ts`, one
  route per spec, abort-safe (`e2e/README.md`, "Authoring conventions").

## Data

- Reads: `useQuery({ ...topicQueries.list(cluster), enabled })` with the
  `queryOptions()` factories in `src/lib/queries/<resource>.ts`, keyed with
  `clusterKey(cluster)`. `src/__checks__/query-keys.test.ts` fails on a
  `queryKey` defined anywhere else. Writes: `useMutation`, invalidate the
  factory keys on success, errors in-page, no optimistic updates on
  destructive actions, never fetch in `useEffect`.
- Endpoint functions live in `src/lib/api.ts` over the typed `openapi-fetch`
  client in `src/lib/api-client.ts`, which sets `X-Kafkito-Cluster` and
  `X-Kafkito-Confirm-Prod`. Types come from `src/lib/api.gen.ts`.
- The active cluster is a path segment, read with `useCluster()`
  (`src/lib/use-cluster.ts`); view state lives in search params;
  `localStorage` keys are namespaced `kafkito.*`.
- Missing backend data renders `—` with a `// TODO(backend): <endpoint> <field>`
  comment and is listed in the PR body. Never fabricate data.

## Design system, distilled

Sources: `src/index.css`, DESIGN_GUIDELINES § 2 to § 9 and § 12.

- Token utilities only: `bg-bg`, `bg-panel`, `bg-subtle`, `bg-hover`,
  `border-border`, `border-border-hover`, `text-text`, `text-muted`,
  `text-subtle-text`, `bg-accent`, `text-accent-foreground`, `text-success`,
  `text-warning`, `text-danger`, `bg-tint-green-bg` with `text-tint-green-fg`
  (amber, red likewise). No hex, `rgb()`, `hsl()` or `oklch()` in components;
  no default palette classes (`bg-slate-50`, `text-white`): the palette is
  disabled in `@theme`, so they generate no CSS.
- Canonical token names, not the transitional aliases `color-surface-*`,
  `color-text-muted`, `color-text-subtle`, `color-text-on-accent`.
- One accent. `text-accent-foreground` on `bg-accent` and `bg-danger`, never
  white (it turns near-black in dark mode). `border-border-strong` marks
  focused or selected state only; hover uses `border-border-hover`.
- Focus comes from the global `:focus-visible` outline in `src/index.css`.
  No `ring-*` for focus, no `outline-none`, no `focus:border-*`.
- Status is never colour alone: `StatusDot`, `StatusIcon`/`StatusBox`,
  `StateBadge`, `LagBadge`, a word or a shape, with the meaning in the
  accessible name. Lag thresholds exist once, in `lagVariant()` in
  `src/lib/format.ts`; feature code uses `LagBadge`.
- `font-mono` and `tabular-nums` for Kafka identifiers (topics, groups,
  subjects, principals, broker ids, offsets, partitions, hosts) and numbers;
  numbers right-aligned in tables. Dates only through `<Timestamp>`
  (`src/__checks__/date-format.test.ts`).
- Destructive actions open `<ConfirmDialog confirmPhrase>` so the user types
  the name. Never `window.confirm`, `alert` or `prompt`.
- Icons from `lucide-react` only: `h-4 w-4` inline and in buttons, `h-5 w-5`
  standalone, `h-3.5 w-3.5` for status glyphs and glyphs inside controls,
  larger only in `EmptyState`/`ErrorState`. Icon-only buttons are
  `<IconButton aria-label>`.
- Shape and density: containers `rounded-xl`, controls `rounded-md`, tags
  `rounded-sm`, dots `rounded-full`; flat `border border-border`, shadows only
  on floating surfaces (`shadow-xl` modals and popovers, `shadow-lg` tooltips
  and toasts); page root `space-y-5 p-6`, controls `h-9`, table cells
  `px-4 py-2.5`. Data pages run edge to edge; forms and settings may cap at
  `max-w-3xl mx-auto`.
- Every view ships five states: loading (skeletons, no full-page spinner),
  empty (`EmptyState`), error (`ErrorState` with retry), degraded (amber
  `Notice` naming the missing permission, or a one-time toast via
  `claimOncePerSession` when only one column is affected), populated. Check
  each in light and dark (`html.dark`); every token exists in both.
- Copy: English, sentence case, written inline; `kafkito` is always
  lowercase; no emojis in UI chrome; `—` for missing values; counts joined
  with ` · `. Uppercase only for eyebrows, table headers, form labels and tags.
- Accessibility gates: Biome a11y rules in `bun run lint`, token-pair contrast
  in `src/__checks__/contrast.test.ts`, axe in `e2e/a11y.spec.ts`
  (moderate and above fail). One `<h1>` per page (`PageHeader`), sections
  start at `<h2>`; sortable headers carry `aria-sort`; disabled controls say
  why via `aria-describedby`.

## Changing tokens or primitives

1. Define a token in both `@theme` and `html.dark` in `src/index.css`;
   document it in DESIGN_GUIDELINES § 2.1 (tokens) or § 6.1 (primitives); add
   a pair to `src/__checks__/contrast.test.ts` for a new foreground/background
   combination.
2. Add a row to DESIGN_GUIDELINES § 14.
3. Note in the PR that the design-system artifact must be re-synced. It is a
   snapshot; never edit it from the repository.

`rounded-sm` is 4 px (Tailwind v4); the code and the design-system artifact
use that value, and DESIGN_GUIDELINES § 4.3 records it.

## Design-system artifact (optional)

The "kafkito" Design System artifact at
https://claude.ai/artifact/SawduEC61hF6fVRFuQ5Gnt is a private snapshot
extracted from `main@5286408` (tokens in light and dark, type scale, spacing,
radii, shadows, a brand book, 29 primitives and 5 pieces of app chrome).
Claude sessions with access read `project/README.md`, `project/tokens.json`,
`project/01-components.md`, `project/02-accessibility.md` and
`project/components/<Name>/README.md` with the Artifact tool. Other agents
skip it; the repository sources above stay canonical either way.
