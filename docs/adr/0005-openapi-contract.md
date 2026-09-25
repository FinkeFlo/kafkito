# ADR-0005: OpenAPI 3.1 as the HTTP contract

- **Status:** Accepted
- **Date:** 2026-09-25
- **Supersedes:** the "RPC layer" row of [ADR-0002](0002-tech-stack.md)

## Context

ADR-0002 chose Connect-RPC with `buf` (protobuf-first) as the RPC layer.
In practice only one procedure, `InfoService.GetInfo`, was ever built on it.
It duplicated `GET /api/v1/info`, and nothing called `/rpc`. All real
functionality (about 40 routes) is hand-written chi REST handlers.

That left three unsynchronised descriptions of the same payloads:
1. Go DTO structs in `internal/kafka` and `internal/server` (the actual
   behaviour),
2. a hand-maintained `openapi.yaml` served at `/api/v1/openapi.yaml`, which
   covered 27 of the paths and had drifted in field names, envelopes, status
   codes and error shapes,
3. hand-written TypeScript interfaces in `frontend/src/lib/api.ts`.

The API mainly serves the kafkito UI. The secondary consumers are operators
scripting with `curl`/`jq`, so a plain HTTP/JSON surface matters more than
RPC features such as bidirectional streaming.

## Decision

- **Remove Connect-RPC.** Delete `proto/`, `buf.yaml`, `buf.gen.yaml`, the
  generated `pkg/proto` code and the `/rpc` mount. `/rpc` stays a reserved
  backend prefix so that stale clients get a JSON 404 instead of the SPA
  shell.
- **The spec is the contract.** An OpenAPI 3.1 document at
  `api/openapi.yaml`, maintained by hand, is the single source of truth for
  the HTTP contract. The Go handlers remain the source of truth for
  behaviour; when the two disagree, the spec is corrected (or the handler
  fixed deliberately). The `api` Go package embeds the file, and it is served
  at `GET /api/v1/openapi.yaml`.
- **Generated frontend types.** `openapi-typescript` generates
  `frontend/src/lib/api.gen.ts` (`bun run api:generate` /
  `make api-generate`). The file is committed and excluded from Biome.
  `api.ts` keeps its fetch wrappers and exported type names, but these are
  now aliases of the generated schemas.
- **Gates.**
  - Redocly lint of the spec (`make api-lint`, CI).
  - A drift check: regenerate the types and `git diff --exit-code`
    (`make api-check`, CI).
  - A Go test (`internal/server/openapi_contract_test.go`). It validates the
    spec with kin-openapi and asserts that every chi route has a matching
    path and method in the spec, and the reverse.
  - An `oasdiff` breaking-change check on pull requests, which fails on
    ERR-level changes.
- **Deferred.** Generating Go server interfaces (oapi-codegen strict-server)
  is out of scope here and needs a follow-up decision.

## Consequences

**Positive**
- One contract instead of three drifting copies. The frontend compiler now
  flags spec changes that affect the UI.
- The new route/spec parity test fails CI when a route is added without a
  spec update. Breaking changes to clients are visible in PRs.
- Dependencies shrink: no `connectrpc.com/connect`, no protobuf runtime and
  no `buf` toolchain.
- The API stays `curl`-friendly, and the spec works with standard OpenAPI
  tooling.

**Negative**
- The spec is still hand-written, so the Go side is only guarded at the
  route and method level. Request and response shapes can still drift from
  the Go structs until server codegen (or response validation in tests) is
  adopted.
- `openapi-typescript` needs the TypeScript 5 JS API, which the project's
  TypeScript 7 does not provide. It therefore runs through a pinned `bunx`
  instead of as a devDependency.
- Contributors must edit YAML and regenerate types when they change the API.

## Alternatives considered

- **Full Connect-RPC migration** (move every REST handler to protobuf
  services). Rejected: weeks of work, and a less `curl`-friendly API.
  Server streaming in browsers is limited compared with the existing SSE
  endpoints.
- **Code-first OpenAPI generation** (e.g. `huma`, which generates the spec
  from Go handlers). Not chosen for now: it means rewriting every handler
  against a new framework. The spec-first approach can later be completed
  with oapi-codegen without changing the contract.
- **Keep the status quo** (hand-written TypeScript plus an unchecked spec).
  Rejected: the drift found while reconciling the spec (wrong field names,
  missing envelopes, wrong status codes, five undocumented routes) showed
  the cost.
