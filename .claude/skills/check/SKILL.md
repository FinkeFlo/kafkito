---
name: check
description: Pick and run the right local check for the files changed on this branch (Go package tests with the right build tags, golangci-lint, make api-check, Biome/tsc/knip/Vitest for the frontend, actionlint for workflows) and run the full `make check` gate before a commit or pull request. Use when asked to run tests, lint or "the checks", and before reporting any change as done.
---

# Run the right checks

## 1. Find what changed

```bash
git status --porcelain
git fetch -q origin main && git diff --name-only origin/main...HEAD
```

## 2. Fast, targeted checks per path

| Changed | Run |
|---|---|
| `internal/<pkg>/**/*.go`, `cmd/**` | `go test -race -count=1 ./internal/<pkg>/...` then `bin/golangci-lint run ./internal/<pkg>/...` (`make lint-install` once if `bin/golangci-lint` is missing). Files behind `//go:build btp` (`internal/auth/xsuaa/`, `cmd/kafkito/main_btp.go`, `internal/auth/mode_xsuaa_test.go`) need `-tags btp` / `--build-tags=btp`; files behind `//go:build devauth` (`internal/auth/mode_devauth*.go`, `internal/server/userapi_devauth.go`) need `-tags devauth`. |
| Any `.go` file | `bin/golangci-lint fmt <file>` (the PostToolUse hook does this after each edit). |
| `api/openapi.yaml`, `api/oapi-codegen*.yaml` | `make api-generate`, then `make api-check`, then `go test -race -count=1 ./internal/server/ -run 'TestSpecOperations|TestRouter|TestAPIOps|TestSpec'`. A changed response or request type also needs the frontend: `cd frontend && bun run lint`. |
| `frontend/src/**`, `frontend/e2e/**`, `frontend/*.ts`, `frontend/*.json` | In `frontend/`: `bun run lint`; `bun run test <path>.test.ts(x)` for the touched module; `bun run test` whenever `src/index.css`, `src/lib/queries/`, `src/routes/` or date/colour code changed (the checks in `src/__checks__/`); `bun run knip` after adding or removing files, exports or dependencies; `bun run build` after touching routes or config. |
| `frontend/src/content/changelog.ts` | `cd frontend && bun run test src/content/changelog.test.ts`; for a release also `make release-check VERSION=vX.Y.Z`. |
| `.github/workflows/**`, `.github/actions/**` | `make actionlint`. |
| `Makefile` (`GOLANGCI_LINT_VERSION`) or `ci.yml` (golangci-lint-action version) | `make lint-version-check`. |
| `go.mod`, `go.sum` | `make tidy && git diff --exit-code go.mod go.sum`. |
| `.claude/hooks/*.sh` | `shellcheck -s bash .claude/hooks/*.sh`, then the sample `echo '<json>' | hook.sh` line from the script header. |
| `docs/**`, `*.md` | No gate. A new page under `docs/` needs a `mkdocs.yml` nav entry. |

Run the smallest set first and read the first failure before anything else.

## 3. Before a commit or PR

```bash
make check
```

`make check` = `test lint lint-version-check api-check frontend-check`. It
must be green. Fix the cause: regenerate on drift (`make api-generate`),
format on formatter diffs, never skip or disable a test, never loosen a rule.

## 4. What this environment cannot run

`make e2e`, `make test-integration`, `make dev`, `make docker-build` and
`make release-snapshot` need Docker. Without it (Claude Code cloud sessions),
say so in the PR test plan; CI runs `make e2e` in `e2e.yml` and the Docker
builds in `ci.yml`.
