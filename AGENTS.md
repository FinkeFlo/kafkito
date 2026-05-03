# Kafkito — Agent Instructions

Short context for AI agents (Copilot CLI, Claude Code, Cursor, coding-agent PRs).

## Stack

- **Backend**: Go (`cmd/kafkito`, `internal/`, `pkg/`), gRPC + REST.
- **Frontend**: React 19 + TypeScript, Vite, TanStack Router (file-based, generated `routeTree.gen.ts`), TanStack Query, Tailwind v4, `lucide-react` icons, `clsx` + `tailwind-merge`. Package manager: **bun**.
- **Build/Test**: `make build`, `make test`, `make lint` (Go). Frontend: `cd frontend && bun run build` / `bun run dev`.

## Repo layout

```
cmd/        Go entrypoints
internal/   Go business logic
pkg/        Go shared/exported
proto/      gRPC definitions
frontend/   React app
  src/components/  UI primitives & shared components
  src/routes/      TanStack file-based routes
  src/lib/         api.ts, utils.ts (cn helper)
docs/       Plans, reviews, ADRs
```

## Conventions (all areas)

- **UI string language**: English. Code comments also English.
- **No emojis** in UI, logs, or commit messages.
- **Surgical edits**: respect the existing style, do not smuggle in unrelated refactors.
- After changes: run the relevant linters/builds (`make lint`, `make test`, `bun run build`).
- **Target devices (frontend)**: desktop and iPad (≥ 768 px). **No mobile-first**, no burger menu, no mobile layout.
- **External libraries**: before using new library APIs, consult current documentation to use up-to-date syntax and recommended patterns. Applies to React 19, TanStack Router/Query, Tailwind v4, `lucide-react`, `sonner`, and Go libraries on the backend side. Details in the frontend style guide § 13.
- **Live verification (frontend)**: every UI change is checked during development against the running dev server (navigation, snapshot, console, interaction, screenshot). Details in the frontend style guide § 15.

## Specific style guides

When working on frontend code (`frontend/**`) the following are binding:

- **`docs/DESIGN_GUIDELINES.md`** — tokens, components, page scaffolding, states. **Source of truth**. The checklist in § 11 must be ticked before every frontend PR.

For design decisions not covered there, research the existing component
inventory and route patterns first.

## Reuse before rebuild

Before creating a new component, check `frontend/src/components/` for a
matching primitive. New primitives (Badge, Button, Card, Table,
EmptyState, …) belong there, not in individual routes.

## Always true

- UI strings and code comments are English only. No emojis in UI chrome, logs, or commit messages.
- Frontend: use Tailwind utilities generated from `@theme` tokens (`bg-panel`, `text-muted`, `border-border`, `text-accent`, `bg-tint-green-bg`, …). Never use default Tailwind palette classes (`bg-slate-*`, `text-gray-*`, `border-zinc-*`) in new code.
- **Hard gate for frontend work:** before finishing any turn that touched `frontend/`, `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run test` must all pass.
- When a design references a field the backend does not expose, render `"—"` and leave a `// TODO(backend): …` comment. Surface every such TODO in the PR body.
