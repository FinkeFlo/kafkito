# Kafkito Frontend Design Guidelines

**Status:** canonical · **Owner:** design · **Applies to:** `frontend/`

This document is the source of truth for the kafkito frontend UI.
All new pages, components, and features must follow it.

It is written to be read by **both humans and coding agents**
(Claude Code, GitHub Copilot, Copilot CLI, Cursor, etc.). Agents: treat
every rule below as a hard constraint unless a rule explicitly says
"prefer" or "may".

---

## 0 · TL;DR for agents

Before writing any frontend code, do these five things:

1. Read this file end-to-end.
2. Open `frontend/src/index.css` to confirm the active design tokens.
3. Open the closest existing route under `frontend/src/routes/` and mimic
   its structure (imports order, query setup, layout scaffold).
4. Use Tailwind utilities generated from `@theme` tokens
   (e.g. `bg-panel`, `text-muted`, `border-border`). Never hard-code hex
   values, never use the default Tailwind palette classes (`bg-slate-50`,
   `text-gray-700`, etc.) in new code.
5. Never fabricate backend data. If a field does not exist on the API
   type, render `"—"` and add a `// TODO(backend): …` comment.

---

## 1 · Stack (immutable)

| Concern | Choice | Notes |
|---|---|---|
| Framework | React 19 + TypeScript 5.7 | function components, no class components |
| Build | Vite 6 | |
| Routing | TanStack Router (file-based) | `src/routes/*.tsx` — never hand-edit `routeTree.gen.ts` |
| Data | TanStack Query v5 | every server read goes through `useQuery`; every write through `useMutation` |
| Styles | Tailwind v4 via `@tailwindcss/vite` | tokens in `src/index.css` under `@theme` |
| Icons | `lucide-react` | no other icon libraries |
| Class merging | `clsx` + `tailwind-merge` | re-export as `cn()` in `src/lib/utils.ts` |
| Dark mode | class-based (`html.dark`) | never media-query-only |

**Do not add** shadcn, Radix, Headless UI, Mantine, Chakra, Material, CSS-in-JS,
styled-components, emotion, a state library (Redux/Zustand/Jotai), or a
charting library. If you think you need one, stop and open a discussion
first.

---

## 2 · Design tokens

Tokens live in `frontend/src/index.css` under `@theme`. They are the only
way to introduce color, font-family, or semantic intent.

### 2.1 Color tokens

See `src/index.css` for the canonical values. Category summary:

| Category | Tokens | When to use |
|---|---|---|
| Surface | `bg`, `panel`, `subtle`, `hover` | page bg, card bg, striped row bg, hover bg |
| Border | `border`, `border-hover`, `border-strong` | `border` is the default hairline; `border-hover` is the hover affordance (e.g. inputs, secondary buttons); `border-strong` is reserved for the focused/selected state |
| Text | `text`, `muted`, `subtle-text` | primary / secondary / tertiary |
| Accent | `accent`, `accent-hover`, `accent-subtle`, `accent-foreground` | primary CTA, active nav, focused input; `accent-foreground` is the canonical foreground colour on saturated `bg-accent` / `bg-danger` fills |
| Focus | `focus`, `focus-on-accent` | global focus-indicator colour (`focus`) and the override colour used by primitives that sit on saturated backgrounds (`focus-on-accent`) |
| Semantic | `success`, `warning`, `danger` | status dots, validation, destructive actions |
| Overlay | `overlay` | modal / dropdown scrim — defined for both modes |
| Tint | `tint-{green,amber,red}-{bg,fg}` | table cell / badge highlights |

Notes:

- `text-text-on-accent` is a transitional alias of `text-accent-foreground`. Treat `accent-foreground` as canonical; the alias may be removed in a future release.
- `border-strong` is **never** used for hover affordances any more — that role belongs to `border-hover`. Custom `focus:border-…` rules on inputs are an anti-pattern (see § 9 — let the global `:focus-visible` rule handle it).
- `focus-on-accent` is consumed by `<Button variant="primary">` and `<Button variant="danger">` to override `outline-color` so the focus indicator stays AA-legible against saturated fills.
- `overlay` is markedly darker in light mode than the original Direction-A spec. The 2026-04 sweep moved the light value into the modern modal-dimmer range to fix a dark-mode invisible-scrim regression; the heavier scrim is intentional in both modes.

### 2.2 Using tokens

**Do:**

```tsx
<div className="rounded-xl border border-border bg-panel p-4">
  <p className="text-sm text-muted">Last synced 2m ago</p>
</div>
```

**Don't:**

```tsx
// ❌ default Tailwind palette
<div className="rounded-xl border border-slate-200 bg-white p-4">
// ❌ inline style with hex
<div style={{ background: "#ffffff", borderColor: "#e5e7eb" }}>
// ❌ inline style with var() when a utility exists
<div style={{ background: "var(--color-panel)" }}>
```

The only case where `style={{ color: "var(--color-xxx)" }}` is acceptable
is when the value is **data-driven** (sparkline stroke color per series,
gauge bar width, etc.) and no utility would work.

### 2.3 Dark mode

Dark mode is a single class on `<html>`. Do not gate components on
`useTheme()` state for styling — CSS custom properties under `html.dark`
swap everything automatically.

If you absolutely need a different element (different icon, different
illustration), use the dark variant:

```tsx
<img src="/logo-light.svg" className="block dark:hidden" />
<img src="/logo-dark.svg"  className="hidden dark:block" />
```

### 2.4 Adding a new token

1. Propose it in the PR description with justification.
2. If accepted, add it to `@theme` in `src/index.css` for both light and
   `html.dark`. Both modes must define it.
3. Document it in this file under § 2.1.

Do not ship one-off hex values. Every color must be a token.

---

## 3 · Typography

| Role | Class | Use |
|---|---|---|
| Page title (h1) | `text-2xl font-semibold tracking-tight` | once per page |
| Section title (h2) | `text-sm font-semibold` | card headers |
| Eyebrow | `text-[11px] font-semibold uppercase tracking-wider text-muted` | above h1 for breadcrumb/context |
| Body | `text-sm` | default |
| Body muted | `text-sm text-muted` | helper text |
| Table cell (text) | `text-sm` | |
| Table cell (mono) | `font-mono text-[13px]` | IDs, names, offsets |
| Table header | `text-[11px] uppercase tracking-wider text-muted` | |
| KPI value | `text-2xl font-semibold tabular-nums` | |
| Small mono label | `font-mono text-[11px] text-muted` | hostnames, IDs in captions |

**Rules:**

- Always pair numeric data with `tabular-nums`.
- Always use `font-mono` for Kafka identifiers (topics, groups, subjects, principals, broker IDs, offsets, partitions, hostnames).
- Never use font sizes not in the table above. If you think you need one, use one of the existing sizes.
- Line-height follows Tailwind defaults; override with `leading-relaxed` only for `<pre>` blocks of code/schema.

---

## 4 · Spacing & layout

### 4.1 Page scaffold

Every route renders as children of `<Shell>` (in `src/components/Shell.tsx`).
The page body itself follows this pattern:

```tsx
export function Route = createFileRoute("/my-page")({
  component: MyPage,
});

function MyPage() {
  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow={`${cluster} › My things`}
        title="My things"
        subtitle="Count + brief context"
        actions={<Button>+ New</Button>}
      />
      <Toolbar />
      <DataCard />
    </div>
  );
}
```

- Page root: `space-y-5 p-6` (20 px vertical rhythm, 24 px gutter). The longer form `space-y-5 px-6 py-6` compiles to identical CSS and is acceptable in existing routes; new code should prefer the shorter `p-6`.
- Multi-section pages: wrap each section in `space-y-6` if you need more breathing room
- **Never** cap width (`max-w-6xl`, `container`, etc.) on data-dense pages. Tables, toolbars, and KPI grids go edge-to-edge within the shell.
- Content-heavy pages (settings, empty states, forms) may cap: `max-w-3xl mx-auto` is the standard width.

### 4.2 Grid & flex

- KPI strips: `grid grid-cols-4 gap-3` (4 cards); `grid-cols-2` on narrow viewports — add a simple breakpoint (`md:grid-cols-4`)
- Toolbars: `flex items-center gap-2`
- Master/detail: `grid grid-cols-[420px_1fr] gap-4`
- Form fields: `grid grid-cols-2 gap-3` for inline fields, stacked otherwise

### 4.3 Radii

| Element | Class |
|---|---|
| Card / table / panel | `rounded-xl` (12 px) |
| Button / input / dropdown | `rounded-md` (6 px) |
| Pill / tag / tint cell | `rounded-sm` (2 px) |
| Status dot / avatar | `rounded-full` |

`rounded-md` is the canonical button radius — `<Button>`, `<IconButton>`, `<Input>`, and `<Notice>` all consume it. Do not introduce `rounded-lg` or `rounded-2xl` on these primitives.

### 4.4 Borders & surfaces

- Flat design: no `shadow-*` on cards or buttons; use `border border-border`. `<Card>` and `<Button>` no longer ship `shadow-sm`.
- Modals and dropdowns: the one exception — `shadow-xl` on floating surfaces over a `bg-overlay` backdrop.
- Focus indicator: the canonical pattern is the global `:focus-visible` rule
  (`outline: 2px solid var(--color-focus); outline-offset: 2px;` declared in
  `index.css`). Tailwind's `ring-*` utility is implemented via `box-shadow`,
  which is clipped by `overflow:hidden` ancestors (tables, modals, tinted
  panels) and silently fails WCAG 2.4.13 — so `outline + outline-offset` is
  preferred over `ring-*` for focus, and primitives that sit on saturated
  fills (`<Button variant="primary">`, `<Button variant="danger">`) override
  `outline-color` to `var(--color-focus-on-accent)` for AA contrast.
- `outline` is reserved for focus indicators — do not use `outline` as
  decorative chrome on idle elements.

### 4.5 Density

Kafkito is a data console. Err toward density, not whitespace.

- Table row padding: `px-4 py-2.5` (content), `px-4 py-2` (header)
- Card padding: `p-4` (standard), `p-5` (overview/hero)
- Form control height: `h-9` (36 px), inputs use `px-3`
- Buttons: `px-3 py-1.5 text-xs font-semibold` for toolbar buttons; `px-3 py-1.5 text-sm font-semibold` for primary CTAs

---

## 5 · Shell / navigation

Every route is wrapped by `<Shell>` in `src/components/Shell.tsx`. Do not
bypass it.

### 5.1 What Shell provides

- Header with logo, cluster pill, ⌘K search, timezone chip, theme toggle, user chip
- Underlined tab bar for primary nav
- `<Outlet />` for the active route
- Manages theme (light/dark) via `useTheme()` in `src/lib/theme.ts`
- Manages global cluster selection via URL search param `?cluster=`

### 5.2 Adding a top-level route

1. Add the file: `src/routes/my-page.tsx`
2. Add the nav entry in `Shell.tsx` **only if** the route should be
   globally visible. Secondary routes (Settings, Brokers) stay out of the
   primary tab bar and are reached via the cluster detail page or an
   overflow menu.
3. Run `npm run routes:generate` (or rely on `npm run build`).
4. Commit `routeTree.gen.ts`.

### 5.3 Cluster selection

All multi-cluster-aware routes accept `?cluster=<name>` as a search
param. Schema:

```tsx
export const Route = createFileRoute("/my-page")({
  validateSearch: (s: Record<string, unknown>) => ({
    cluster: typeof s.cluster === "string" ? s.cluster : undefined,
  }),
  component: MyPage,
});
```

Read in the component:

```tsx
const { cluster } = Route.useSearch();
```

**Never** introduce a per-page cluster picker. The pill in the header is
the only way to change the active cluster.

---

## 6 · Components

Every reusable component lives under `frontend/src/components/` and is
imported from there. Shared primitives must not be duplicated across
routes.

### 6.1 Core primitives (already exist — use them)

| Component | Purpose |
|---|---|
| `<Shell>` | Header + nav + outlet |
| `<PageHeader>` | `eyebrow?` + `title` + `subtitle?` + `actions?` |
| `<KpiCard>` | `label` + `value` + `unit?` + `delta?` (optional `trend` consumes `<Sparkline>`) |
| `<Tag>` | Small mono tag, `variant="neutral" \| "info"` |
| `<StatusDot>` | 2×2 colored circle |
| `<StateBadge>` | Consumer-group state pill |
| `<Sparkline>` | 80×24 SVG sparkline (consumed by `<KpiCard>` via the `trend` prop; available for future dashboard work) |
| `<Gauge>` | Horizontal usage bar (available for future capacity/utilisation widgets) |
| `<DataTable>` | Styled `<table>` with built-in `<thead>` / row styles, sort headers, and skeleton/empty body states |
| `<Toolbar>` | Filter / action row: `search?` (left), `filters?` (centre), `actions?` (right, `ml-auto`). Replaces hand-rolled `flex flex-wrap items-center gap-2` blocks |
| `<EmptyState>` | Icon + heading + CTA |
| `<ErrorState>` | Icon + detail + retry |
| `<Modal>` | `open` + `onClose` + `title` + `children` + `actions?` + `size?` (`sm \| md \| lg`) + `ariaDescribedBy?`; centered panel with backdrop, focus-trap, body-scroll lock, Escape-to-close, focus-restore |
| `<Notice>` | `intent="info" \| "success" \| "warning" \| "danger"` + `title?` + `children` + `icon?` + `actions?`; tinted callout for degraded-capability banners and inline explanations. Always pairs colour with an icon |
| `<Button>` | `variant="primary" \| "secondary" \| "danger" \| "ghost"` + `size="sm" \| "md"` + `leadingIcon?` / `trailingIcon?` + `loading?` |
| `<Input>` | `h-9` text input + `invalid?` (switches border to `border-danger`) + `leadingIcon?` / `trailingIcon?`. Does **not** set `outline-none`; the global `:focus-visible` rule is the focus indicator |

If it's not in the list and doesn't exist yet, **write it first as a
component**, then use it. No inlined duplications of table chrome, modal
scaffolding, button styles, etc.

### 6.2 Buttons

| Variant | Visual | When |
|---|---|---|
| `primary` | `bg-accent text-accent-foreground hover:bg-accent-hover` (with `focus-visible:[outline-color:var(--color-focus-on-accent)]`) | the one main action per screen |
| `secondary` | `border border-border bg-panel text-text hover:bg-hover hover:border-border-hover` | everything else |
| `danger` | `bg-danger text-accent-foreground hover:bg-danger/90` (with `focus-visible:[outline-color:var(--color-focus-on-accent)]`) | destructive only |
| `ghost` | `text-muted hover:text-text hover:bg-hover` | inline, icon-only, overflow menus |

The variant union is exactly `primary | secondary | danger | ghost`. The `destructive` alias was removed at the end of Phase 3 — do not reintroduce it.

One primary button per page. Multiple primaries signal unclear hierarchy.

### 6.3 Tables

All tables use `<DataTable>`. Rules:

- Numeric columns: `text-right tabular-nums`, `font-mono` if they are IDs/offsets
- Name columns: `font-mono text-[13px]`
- Row hover: `hover:bg-hover`
- Clickable rows: cursor-pointer, full-row `<Link>` or `onClick`
- Empty result: show a single row spanning all columns with `text-center text-muted py-8` and a "No results" message
- Loading: replace `<tbody>` with 5 skeleton rows
- Error: render `<ErrorState>` instead of the table

### 6.4 Forms

- Every input has a label above it (`text-xs font-semibold uppercase tracking-wider text-muted`).
- Validation errors: red text below the field, and a semantic border on the input (`border-danger`).
- Destructive actions require a `<Modal>` / `<ConfirmDialog>` — never a single click, never `window.confirm`.
- Disabled controls must convey *why* they are disabled via `aria-describedby` plus either a visible `<Notice>` or an `sr-only` `<span>`. Never rely on `title=` alone for load-bearing reason copy — the disabled-with-tooltip pattern is a Confluent anti-pattern (see § 12).
- After mutation, invalidate the matching TanStack Query keys. Never refetch manually.

### 6.5 Status conventions

| State | Color |
|---|---|
| Reachable / stable / allow | `success` (or `tint-green`) |
| Warning / rebalancing / lag 1–5k | `warning` (or `tint-amber`) |
| Unreachable / dead / lag > 5k / deny / destructive | `danger` (or `tint-red`) |
| Unknown / empty / internal | `muted` |

Thresholds (lag 5 000, CPU 70 %, disk 80 %) live in `src/lib/format.ts` as
named constants — use them, never hard-code.

### 6.6 Iconography

- Always import from `lucide-react`.
- Icon size: `h-4 w-4` inline with text, `h-5 w-5` standalone, never larger unless for empty-state illustrations.
- Strokes inherit `currentColor`; set color via the parent's `text-*` utility.
- Never mix emoji into UI chrome. Unicode glyphs (`›`, `·`, `⌕`) are fine for separators and inline hints.

---

## 7 · Data, state, and side effects

### 7.1 Fetching

```tsx
const q = useQuery({
  queryKey: ["topics", cluster],
  queryFn: () => fetchTopics(cluster!),
  enabled: !!cluster,
  refetchInterval: 10_000, // only if the data is "live"
});
```

- Keys are arrays of `[<resource>, ...scopes]`.
- Never fetch in `useEffect`.
- Never store fetched data in component state.
- `refetchInterval` is opt-in — only for data the user watches in real
  time (cluster health, group lag, message tail). Static data does not
  poll.

### 7.2 Mutations

```tsx
const qc = useQueryClient();
const m = useMutation({
  mutationFn: () => createTopic(cluster, req),
  onSuccess: () => qc.invalidateQueries({ queryKey: ["topics", cluster] }),
  onError: (e: Error) => setErr(e.message),
});
```

- Always invalidate affected keys on success.
- Always surface errors in-page — never `alert()`.
- Optimistic updates are forbidden for destructive ops (delete topic,
  reset offsets). User must see the real outcome.

### 7.3 Client state

- URL is the source of truth for view state that should survive reload:
  active cluster, filters, selected row, open tab. Use search params.
- Ephemeral state (modal open, unsubmitted form values): `useState`.
- Cross-route state that doesn't belong in the URL: `localStorage`
  under the namespace `kafkito.*` (e.g. `kafkito.theme`,
  `kafkito.tailBuffer`). Never unnamespaced keys.

### 7.4 Missing backend data

If the design shows a column the backend doesn't expose:

```tsx
<td className="px-4 py-2.5 text-right font-mono tabular-nums">
  {/* TODO(backend): expose per-topic msg rate on /api/v1/clusters/:cluster/topics */}
  —
</td>
```

- Render `"—"` (em dash). Never `0`, never `"n/a"`, never blank.
- Always leave a comment in the form `// TODO(backend): <endpoint> <field>` (or `<endpoint>` and a short note on the missing field). Every em-dash that stands in for missing data needs a marker — em-dashes without a `TODO(backend):` next to them are bugs.
- Surface every such TODO in the PR body.

---

## 8 · States every page must handle

Every data-driven view ships **all five** states. A page is not done
unless each has been implemented and visually checked.

1. **Loading.** Skeleton rows for tables; skeleton cards for KPI strips.
   No full-page spinners.
2. **Empty** (data loaded, zero rows). `<EmptyState>` with an icon, a
   clear sentence explaining why it's empty, and a CTA where
   appropriate.
3. **Error** (fetch failed). `<ErrorState>` with the error message and a
   retry button. Never silent-fail.
4. **Degraded** (partial capability, e.g. missing ACLs in Kafka). Amber
   `<Notice>` at the top of the view explaining which permission is
   missing and how to fix it. This is a kafkito-specific pattern — see
   existing `limited` code paths in `groups.tsx` and `topics.tsx` for
   precedent.
5. **Populated.** The happy path.

---

## 9 · Accessibility

- Every interactive element has a visible focus indicator from the global
  `:focus-visible` rule in `index.css`
  (`outline: 2px solid var(--color-focus); outline-offset: 2px;`).
  Primitives that sit on saturated backgrounds (primary / danger Button)
  override `outline-color` to `var(--color-focus-on-accent)` for AA
  contrast. Custom `focus:border-…` rules on inputs are an
  anti-pattern — let the global rule handle it; do not reach for
  `focus-visible:ring-2` either (Tailwind's `ring-*` is a `box-shadow`
  and is clipped by `overflow:hidden` ancestors — see § 4.4).
- Every icon-only button has `aria-label`.
- Every modal traps focus and closes on `Escape`.
- Every status color is paired with a text label or icon — never
  color-alone signaling.
- Disabled controls must explain *why* they are disabled via
  `aria-describedby` + a visible `<Notice>` or an `sr-only` `<span>`. Do
  not rely on `title=` alone for load-bearing reason copy.
- Tables that can be sorted or filtered announce that through the column
  header's `aria-sort` attribute.
- Contrast: all tokens are validated for WCAG AA at the token level.
  Don't stack `muted` text on `subtle` background — it drops below AA.
  (The `check:contrast` scanner does not currently model this specific
  pair as a usage failure, so the rule remains a hand-enforced
  contract — review it during PR.)

---

## 10 · Code hygiene

### 10.1 File layout

```
src/
├── routes/                  ← one file per route; file-based routing
│   ├── __root.tsx
│   ├── index.tsx
│   ├── topics.tsx
│   └── topics_.$topic.tsx
├── components/              ← shared, reusable UI only
├── lib/
│   ├── api.ts               ← don't touch without a backend reason
│   ├── format.ts            ← formatters + thresholds
│   ├── theme.ts             ← useTheme hook
│   └── utils.ts             ← cn() and tiny helpers
└── index.css                ← tokens only; no component CSS
```

**Never** create `styles/`, `hooks/` (put hooks in `lib/`), `types/`
(types live next to the code that owns them or in `lib/api.ts`),
`assets/` (put images in `public/`).

### 10.2 Naming

- Components: `PascalCase` files and exports.
- Hooks: `useCamelCase`.
- Query keys: always arrays, lowercase strings.
- CSS custom properties: `--color-<role>` or `--font-<role>`; nothing
  else.

### 10.3 TypeScript

- `strict: true` is on; don't turn it off.
- No `any`. Use `unknown` + narrowing, or extend the type in `api.ts`.
- No non-null assertions (`x!`) except immediately after a TanStack
  Query `enabled` guard or a `useMemo` that filters by existence.
- Prefer `type` aliases over `interface` unless you need declaration
  merging.

### 10.4 Lint & build

Every commit must pass:

```bash
cd frontend
npm run lint            # tsc -b --noEmit
npm run build           # tsr generate + tsc + vite build
npm run check:palette   # fails on default Tailwind palette classes in the current diff
```

If any of these fails, the commit is not done.

`check:palette` only looks at added lines in `git diff origin/main...HEAD`
so pre-existing legacy usages (e.g. in `topics_.$topic.messages.tsx`)
do not block you. To scan everything (e.g. after migrating a legacy
route), run `bash frontend/scripts/check-palette.sh --all`.

---

## 11 · Checklist for a new page

Copy this into the PR description and tick each box.

```
- [ ] Route file under src/routes/
- [ ] Search param schema validates every URL-driven piece of state
- [ ] <Shell> wraps the page (automatic via __root)
- [ ] <PageHeader> at the top with eyebrow + title + subtitle + actions
- [ ] Uses tokens (bg-panel / text-muted / border-border / etc.) — no Slate/Gray classes
- [ ] All five states implemented: loading · empty · error · degraded · populated
- [ ] All mutations invalidate matching TanStack Query keys on success
- [ ] Every icon-only button has aria-label
- [ ] Focus rings visible on keyboard-only
- [ ] No color-only signaling (pair with text or icon)
- [ ] Works in light mode
- [ ] Works in dark mode
- [ ] npm run lint passes
- [ ] npm run build passes
- [ ] npm run check:palette passes
- [ ] routeTree.gen.ts regenerated and committed
- [ ] Every TODO(backend): comment is also listed in the PR body
```

---

## 12 · Anti-patterns (don't do these)

1. ❌ Hard-coded hex values, rgb(), or hsl() in component code.
2. ❌ Default Tailwind palette classes (`bg-slate-*`, `text-gray-*`,
   `border-zinc-*`) in new code.
3. ❌ `max-w-6xl` on data-dense pages.
4. ❌ Per-page cluster picker.
5. ❌ Adding a dependency without discussion.
6. ❌ Inline `<style>` tags in TSX files.
7. ❌ Fabricating data to make a design look full.
8. ❌ `alert()`, `confirm()`, or `prompt()` — use `<Modal>` /
   `<ConfirmDialog>` / `useToast()` instead.
9. ❌ `useEffect` for data fetching.
10. ❌ Full-page loading spinners.
11. ❌ Emoji in UI chrome.
12. ❌ Shadow-heavy card styling (we are flat + bordered).
13. ❌ Animation libraries. CSS transitions and `animate-*` utilities are
    enough.
14. ❌ Modals that don't trap focus or don't close on Escape.
15. ❌ Copy that yells. No all-caps sentences; eyebrows and table headers
    are the only exceptions.

---

## 13 · When this file and the code disagree

When this file and the code disagree, follow the resolution recorded in
the most recent design review or PR changelog (see § 14). Where no
review or changelog covers the discrepancy, this file wins — but flag
the gap in a follow-up PR so the next review can record the decision.

Don't add new violations "to match the existing code". If an existing
route deviates and the deviation is not recorded as a kept-deviation in
a review, treat it as drift and bring it in line.

---

## 14 · Changelog

| Date | Change | PR |
|---|---|---|
| initial | Guidelines established alongside Direction A redesign | — |
| 2026-04-26 | Direction-A delivery: WCAG-AA token sweep (focus-on-accent, accent-foreground, border-hover, dark overlay); new primitives (Toolbar, Modal, Input, Notice); kebab/PascalCase consolidation; Button variant rename destructive→danger; PageHeader eyebrow; Incidents-(24h) → Unreachable now semantic fix; outline-based focus indicator | (this PR — link added by author) |

Add a row on every change. Small tweaks to tokens or primitives are
fine; major shifts (new visual language, new nav model) require a design
review.
