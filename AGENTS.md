# AGENTS.md

kafkito is a web console for Apache Kafka: one Go binary serves the React SPA
(embedded with `//go:embed`) and a JSON API under `/api/v1`, and keeps no
state of its own. Cluster definitions come from the config file or, for
private clusters, from each request. This file is the entry point for coding
agents (Codex, Cursor and Copilot read it directly; Claude Code through
`CLAUDE.md`). `frontend/AGENTS.md` adds the frontend and design-system rules.

## Repository map

| Path | What it is |
|---|---|
| `cmd/kafkito/` | `main`; `main_default.go` / `main_btp.go` select the build variant |
| `internal/server/` | chi router, middleware, strict-server handlers; `internal/server/api/server.gen.go` is generated |
| `internal/kafka/` | franz-go/kadm services `Connections`, `Topics`, `Groups`, `Messages`, `Security`, `Clusters`; Schema Registry HTTP client |
| `internal/auth/` (+ `xsuaa/`) | bearer-token validation; modes `off` (devauth builds), `mock`, `oidc`, `xsuaa` (btp builds) |
| `internal/rbac/`, `internal/masking/`, `internal/netguard/`, `internal/connerr/` | RBAC policy, data masking, SSRF guard, connection error classes |
| `internal/config/` | koanf config: defaults, then YAML, then `KAFKITO_*` env |
| `api/openapi.yaml` | the HTTP contract (ADR-0005); `oapi-codegen.yaml` and the overlay drive the Go generator |
| `frontend/` | Bun, React 19, TanStack Router and Query, Tailwind v4; see `frontend/AGENTS.md` |
| `docs/` | MkDocs site: `architecture.md` (security invariants), `DESIGN_GUIDELINES.md`, `adr/`, `API.md` |
| `.github/workflows/` | `ci.yml`, `e2e.yml`, `secret-scan.yml`, `release.yml`, `docs.yml`, `auto-merge-from-owner.yml` |
| `Makefile` | every command below; `make help` lists the targets |

## Commands

Toolchain: Go 1.26+, Bun 1.4+, curl. Docker only for `make dev`, `make e2e`,
`make test-integration`, `make docker-build` and `make release-snapshot`.

| Task | Command |
|---|---|
| Frontend dependencies | `make frontend-install` |
| Pinned golangci-lint into `./bin` | `make lint-install` (version: `GOLANGCI_LINT_VERSION` in the Makefile, same as `ci.yml`) |
| Dev loop (Docker) | `make worktree-init` once, then `make dev`; backend only: `make run-dev` |
| Go tests | `make test` (= `go test -race -count=1 ./...`); btp build: `make test TAGS=btp`; one package: `go test -race -count=1 ./internal/server/...` |
| Go lint | `make lint` (default and `btp` tags); one variant: `bin/golangci-lint run --build-tags=devauth ./...` |
| Format Go | `bin/golangci-lint fmt <file>` (gofmt + goimports) |
| API contract | edit `api/openapi.yaml`, then `make api-generate`; `make api-check` lints the spec and fails on drift of the generated files |
| Frontend gate | `make frontend-check` (= `bun run lint && bun run knip && bun run build && bun run test` in `frontend/`) |
| One frontend test file | `cd frontend && bun run test src/lib/format.test.ts` |
| Workflow lint | `make actionlint` |
| Full gate, before every PR | `make check` (= `test lint lint-version-check api-check frontend-check`) |
| e2e walks (opt-in, Docker, curl, jq) | `make e2e`; see `frontend/e2e/README.md` |
| Release gate | `make release-check VERSION=vX.Y.Z` |

CI runs the same targets: job `go` (matrix of build tags `""`, `btp`,
`devauth`: `go build -tags X ./...`, `make test TAGS=X`, golangci-lint with
`--build-tags=X`, `make lint-version-check`), `frontend` (`make api-check`,
`make frontend-check`), `api-breaking` (oasdiff against the base branch),
`docker-build`, `actionlint`, `dco`, `playwright` (`make e2e`) and the secret
scans.

## Generated files: regenerate, never edit

| File | Generator | Committed |
|---|---|---|
| `internal/server/api/server.gen.go` | `make api-generate` (oapi-codegen, `api/oapi-codegen.yaml` + `api/oapi-codegen.overlay.yaml`) | yes |
| `frontend/src/lib/api.gen.ts` | `make api-generate` (openapi-typescript) | yes |
| `frontend/src/routeTree.gen.ts` | `cd frontend && bun run routes:generate` (TanStack Router; `bun run lint` and `bun run build` run it too) | no, gitignored |
| `frontend/dist/` | `make frontend-build`; `dist/index.html` in git is a placeholder that a local build overwrites, revert it before committing | placeholder only |

## API workflow (ADR-0005, "Adding an endpoint")

1. Describe the operation in `api/openapi.yaml`: `operationId`, parameters,
   request body, every response, and the input rules (enum, pattern, bounds,
   required). Map schemas onto existing Go types in
   `api/oapi-codegen.overlay.yaml` where they exist.
2. `make api-generate`.
3. Implement the `StrictServerInterface` method on `apiServer` in
   `internal/server/<resource>.go`; return the generated response types,
   errors as `apiError`; reach Kafka only through the store interfaces in
   `internal/server/stores.go` (new Kafka operations go on their service in
   `internal/kafka`).
4. Mount the generated wrapper in `internal/server/api_routes.go` on the right
   group with `noRequestBody` or a body limit in front of `g.validate`; add
   the route pattern to `resolvePermission` in `internal/server/rbac.go` (or
   to `rbacExemptRoutes`, deliberately).
5. Add the operation to `apiOps` in `internal/server/generated_routes_test.go`
   and a successful request in `TestAPIOps_Requests`; the contract, router,
   RBAC and private-cluster tests then cover it.
6. Frontend: endpoint function in `frontend/src/lib/api.ts` over the typed
   client, query factory in `frontend/src/lib/queries/`.

Removing or narrowing anything in the spec fails the `api-breaking` job;
prefer additive changes and call out intentional breaks in the PR.

## Backend conventions

- Build tags: none, `btp` (XSUAA, `internal/auth/xsuaa`, `cmd/kafkito/main_btp.go`)
  and `devauth` (auth mode `off`, loopback only). A file behind a tag needs
  its counterpart behind the negated tag; CI builds, tests and lints all three.
- Tests use testify and live next to the code. `internal/auth` splits
  black-box tests (`package auth_test`, `*_test.go`) from white-box tests
  (`*_internal_test.go`, same package); other packages test in-package.
  Fakes live in `internal/server/fakes_test.go` and `fakebroker_test.go`;
  integration tests are behind `//go:build integration` and need Docker.
- Errors flow through `internal/server/apierror.go`: sentinel errors map to
  `apiError{Status, Code, Message}`; messages are static texts or rule names
  and never repeat a submitted value, host name or credential.
- Logging is `log/slog` to stdout. Never log headers, bodies, query strings
  or credentials; private-cluster addresses are logged on purpose, the raw
  `X-Kafkito-Cluster` header never.
- New settings go through `internal/config` with startup validation and a
  row in the README configuration table.

## Security invariants (details in `docs/architecture.md`)

- Every `/api/v1/*` request passes the auth middleware; only `/healthz` and
  `/readyz` are open. Mode `off` exists only in `devauth` builds and binds
  loopback unless `KAFKITO_INSECURE_AUTH_OFF=true`.
- Private clusters arrive base64-encoded in `X-Kafkito-Cluster`, are
  validated and never persisted. The header, its credentials, host names,
  URLs and auth types never appear in logs, error bodies or validation
  messages; `private_cluster_leak_test.go` and
  `private_cluster_names_test.go` pin this. Connection failures are reported
  as fixed classes (`internal/connerr`), never as raw errors.
- `internal/netguard` guards private-cluster hosts twice: a pre-check on the
  definition and a guarded dialer at connect time (loopback, link-local, the
  metadata endpoint, multicast and unspecified addresses are refused).
  Configured clusters are trusted. Keep both layers.
- RBAC: every cluster route has a permission in `resolvePermission` or an
  entry in `rbacExemptRoutes`; a test walks the router. Lists are filtered to
  what the caller may view.
- Body limits run before the OpenAPI validator; Test connection is rate
  limited and `private_clusters.mode` is applied before the header is decoded.
- Masking happens before truncation; a raw download of a masked value is
  refused with `403 value_masked`.
- Every response carries the strict CSP and security headers; `/api`
  responses carry `Cache-Control: no-store`.

## Secrets

Never read, print or commit `.env*`, `*.local.yaml`, `*.local.yml`, `/.local/`,
`deploy/local/`, `*.pem` or `*.key`. Gitleaks (pre-commit), TruffleHog (CI)
and GitHub push protection scan for leaks; if one happens, rotate the
credential first.

## Commits and pull requests

- Every commit is signed off: `git commit -s` (DCO, checked by the `dco` job).
- Subjects follow Conventional Commits, `type(scope): subject`, imperative,
  written for humans: they become the release notes (`feat`, `fix` and
  security changes are listed; `chore`, `ci`, `test`, `style` and
  `docs(changelog)` are not). Scopes in use: `server`, `kafka`, `auth`,
  `config`, `api`, `frontend`, `ui`, `topics`, `e2e`, `docs`, `changelog`,
  `agents`.
- Everything in the repository, in commits and in PRs is English. No emojis.
- PR body: what and why, then a `## Test plan` with the commands run.
  List every `TODO(backend):` added and every deviation from
  `docs/DESIGN_GUIDELINES.md`.
- Open PRs as drafts and leave them drafts: `auto-merge-from-owner.yml`
  enables auto-merge (rebase) for every non-draft PR by the owner, which
  includes PRs an agent opens on the owner's behalf. The maintainer marks a
  PR ready.
- Releases: an entry in `frontend/src/content/changelog.ts`, checked with
  `make release-check VERSION=vX.Y.Z`; the tag is pushed by a human.

## When sources disagree

Code and tests, then ADRs in `docs/adr/` (newest wins; read the amendments:
ADR-0002 still lists Connect-RPC and shadcn in its original tables, both
superseded), then `docs/DESIGN_GUIDELINES.md`, then `README.md`. Fix the
stale document in the same PR when it is small, otherwise list it in the PR.

## Never

- Edit generated files; skip, disable or quarantine a test; loosen a lint rule.
- Add a UI, state or chart library, or any dependency without discussion
  (`docs/DESIGN_GUIDELINES.md` § 1).
- Push tags, force-push, delete remote refs or use `--no-verify`.
- Touch secret files, or paste credentials, tokens or private-cluster
  definitions anywhere.
- Change `GOLANGCI_LINT_VERSION` without the `golangci-lint-action` version in
  `ci.yml` (`make lint-version-check`).
- Commit `frontend/src/routeTree.gen.ts` or build output.
