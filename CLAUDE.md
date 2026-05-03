# Kafkito — instructions for Claude Code

Before touching anything under `frontend/`, read
[`docs/DESIGN_GUIDELINES.md`](./docs/DESIGN_GUIDELINES.md) in full and
follow every rule. The checklist in § 11 must be satisfied before you
open a PR.

For backend work under `cmd/`, `internal/`, or `pkg/`, follow the
conventions of the surrounding Go code. Run `go test ./...` and
`golangci-lint run` (or `make test && make lint`) before committing.

## Always true

- Never add runtime dependencies without explicit approval in the PR description.
- UI strings and code comments are English only. No emojis in UI chrome, logs, or commit messages.
- Frontend: use Tailwind utilities generated from `@theme` tokens (`bg-panel`, `text-muted`, `border-border`, `text-accent`, `bg-tint-green-bg`, …). Never use default Tailwind palette classes (`bg-slate-*`, `text-gray-*`, `border-zinc-*`) in new code.
- **Hard gate for frontend work:** before finishing any turn that touched `frontend/`, `cd frontend && bun run lint && bun run build && bun run check:palette && bun run check:strings && bun run check:tokens && bun run check:routes && bun run test` must all pass. For visible UI work, also spot-check light + dark mode against the running dev server.
- When a design references a field the backend does not expose, render `"—"` and leave a `// TODO(backend): …` comment. Surface every such TODO in the PR body.

Agents other than Claude Code should still treat this file as their
entrypoint — `AGENTS.md` and any other agent-specific files point back
here.
