@AGENTS.md

## Claude Code, frontend

- Use `/ui-change` for a new page or any visible change; it carries the § 11
  checklist. Run the `design-reviewer` subagent on your diff before you report
  the change as done.
- The PostToolUse hooks run `biome check --write` on each edited file and
  `.claude/hooks/design-token-guard.sh` on files under `frontend/src/`. Treat
  their findings like review comments and fix them before `bun run lint`.
- In cloud sessions `bun run lint`, `bun run knip`, `bun run build` and
  `bun run test` run; `make e2e` and `make dev` need Docker and do not. Say so
  in the PR instead of skipping the e2e question silently.
- Prefer Context7 (`.mcp.json`) over memory for TanStack Router and Query,
  Tailwind v4, Biome and Vitest APIs; the pinned versions are in
  `package.json`.
