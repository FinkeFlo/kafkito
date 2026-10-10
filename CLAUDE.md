@AGENTS.md

## Claude Code

The shared configuration under `.claude/` and `.mcp.json` is committed.
Personal overrides go to `.claude/settings.local.json` and `CLAUDE.local.md`
(gitignored). `frontend/CLAUDE.md` loads when you work under `frontend/`.

### Skills (`/name`; Claude also loads them when the task matches)

- `/check`: the right fast check for the paths you changed; `make check`
  before a PR.
- `/api-endpoint`: add or change an API operation (spec, generate, handler,
  mount, `apiOps`, frontend client).
- `/ui-change`: a new page or any visible change: § 11 checklist, five states
  in both themes, a11y, § 14 changelog row for tokens and primitives.
- `/release-prep`: changelog entry and `make release-check`; never pushes a tag.
- `/steward`: pull request conventions and the CI-job-to-make-target map;
  the cloud harness reads it when it drives a PR.
- `/adr`: a new or amended ADR in `docs/adr/`.

### Subagents (read-only reviewers)

Run them on your diff before you report a change as done:
`design-reviewer` for `frontend/src/`, `security-reviewer` for
`internal/auth`, `internal/netguard`, `internal/masking`, `internal/rbac`,
private clusters and error handling, `api-contract-reviewer` for
`api/openapi.yaml`, `internal/server/api_routes.go` and handler signatures.

### Hooks and permissions (`.claude/settings.json`, scripts in `.claude/hooks/`)

- PreToolUse: edits to generated files and secret files are refused with the
  command to run instead; `git commit` without `-s`, `--no-verify`, force
  pushes and tag pushes are refused.
- PostToolUse: the edited file is formatted with the pinned tools
  (`bin/golangci-lint fmt`, `biome check --write`); remaining diagnostics and
  design-token findings for `frontend/src` come back as feedback. Fix them,
  do not argue with them.
- SessionStart (cloud sessions only): installs frontend dependencies, Go
  modules and the pinned golangci-lint, and reports what is unavailable.
- Routine `make`, `go`, `bun run` and read-only `git` commands are
  pre-approved; secret files, generated files, force and tag pushes are denied.
  Each hook script has a sample `echo '<json>' | hook.sh` line in its header.

### Cloud sessions

- No Docker: `make dev`, `make e2e`, `make test-integration`,
  `make docker-build` and `make release-snapshot` do not run here. Say so in
  the PR; `e2e.yml` runs the Playwright walks in CI.
- Everything else in `make check` runs. The SessionStart hook prints what is
  ready. Its golangci-lint step falls back to the verified release archive
  when the egress proxy refuses the install script's tag lookup.
- Two tests in `internal/server` are environment-sensitive here and pass in
  CI: `TestTestCluster_ConnectionFailureNamesNoAddress` expects a dial to
  `192.0.2.1` to time out, but the sandbox refuses the connection at once;
  `TestProduceMessage_BodyLimits` can exceed its produce deadline when the
  whole suite runs under `-race` (re-run it alone: it passes). Report them as
  such in the PR; do not change the tests to make them pass here.
- You act with the repository owner's GitHub identity: open PRs as drafts and
  leave them drafts (see AGENTS.md, "Commits and pull requests").

### MCP

`.mcp.json` registers Context7 (`https://mcp.context7.com/mcp`, no key) for
current library documentation: TanStack Router and Query, Tailwind v4, Biome,
Vitest, Playwright, chi, franz-go, oapi-codegen. Use it before relying on
memory for an API. `.claude/settings.json` pre-approves it
(`enabledMcpjsonServers`), which takes effect once you trust the folder. A
personal API key belongs in your local MCP scope, not in the repository.
