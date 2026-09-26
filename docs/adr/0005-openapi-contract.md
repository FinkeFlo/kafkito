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
- **Generated Go server.** See [Generated strict server](#generated-strict-server)
  below. It was added after the initial decision.

## Generated strict server

- **Status:** Accepted
- **Date:** 2026-09-25

The Go side is generated from the same spec with oapi-codegen v2.8.0, which
supports OpenAPI 3.1 (including `type: [T, "null"]` and `const`).

- **Codegen.** The tool is pinned as a Go tool dependency
  (`go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`)
  and runs as `go tool oapi-codegen`. `api/oapi-codegen.yaml` generates
  models, the `chi-server` wrapper and the `strict-server` interface into
  `internal/server/api/server.gen.go`. `make api-generate` regenerates both
  the TypeScript types and this file, and `make api-check` fails on drift
  in either one. The generated file is excluded from golangci-lint by path.
- **Overlay.** `api/oapi-codegen.overlay.yaml` is applied only by
  oapi-codegen, so the published spec does not carry Go extensions. It maps
  spec schemas onto the existing domain types (`config.ClusterConfig`,
  `kafka.ClusterInfo`, ...), which means the generated server serialises
  exactly what the hand-written handlers returned. It also drops the
  `X-Request-Id` response headers, because the request log middleware
  already sets them.
- **Migration complete.** The operations were migrated in steps; the
  codegen config listed the migrated ones in
  `output-options.include-operation-ids`. Every operation of the spec is
  now generated and served by the strict server, so the list is gone and
  no chi route is written by hand. Two tests keep it that way:
  `TestSpecOperations_AllGeneratedAndMounted` checks that every operation
  id has a strict method and a route wrapper and that a request to its
  path reaches the generated binding of that operation (the SSE
  `copyMessages` included), and `TestRouter_EveryRouteIsASpecOperation`
  walks the chi router and fails on any route that is not a spec
  operation served by its generated binding, and on any operation without
  a route.
- **Per-group registration.** The generated `HandlerWithOptions` would put
  every operation on one router behind one middleware chain. Instead, each
  generated `ServerInterfaceWrapper` method is registered individually on
  the chi group it belonged to before (`internal/server/api_routes.go`):
  the probes have no auth, the meta endpoints run behind auth, and the
  cluster routes also run the private-cluster, RBAC and
  private-cluster-param middleware. The relative paths keep the chi route
  patterns unchanged, because RBAC resolves permissions from those patterns.
  A test asserts the pattern, chain and permission of every operation.
- **Request validation.** Each generated route runs
  `github.com/oapi-codegen/nethttp-middleware` (kin-openapi) after its group
  middleware, so every operation is validated. The rules
  live in the spec only (enum, pattern, minItems, required, ...), and the
  duplicated hand-written input checks were removed. The options are:
  - `AuthenticationFunc = openapi3filter.NoopAuthenticationFunc`:
    authentication and RBAC stay in the existing middleware. `bearerAuth`
    in the spec is documentation only.
  - `DoNotValidateServers`: the server URL differs per deployment.
  - `SkipSettingDefaults`: the validator must not rewrite requests.
  - Order: a body limit (`http.MaxBytesReader`) runs before the validator,
    because kin-openapi buffers the whole body. Routes without a request
    body get an empty body.
  - The validator sees a cleaned URL path, so `/api//v1/info` behaves as
    chi routes it.
- **Central error mapping.** `internal/server/apierror.go` defines one error
  type, `apiError{Status, Code, Message}`, and one `writeError`. It maps
  sentinel errors (`kafka.ErrUnknownCluster`, `kafka.ErrNotAuthorized`,
  `kafka.ErrGroupExists`, ...), parameter-binding errors and validation
  errors to status codes through `errors.As`/`errors.Is`. The strict
  handler's `RequestErrorHandlerFunc` and `ResponseErrorHandlerFunc`, the
  wrapper's parameter-binding error handler and the validator's
  `ErrorHandlerWithOpts` all use it. The JSON body is still the `Error`
  schema (`{"error": ..., "code": ...}`). Only 5xx errors are logged, and
  the cause stays in the log.
- **No value echo.** kin-openapi's messages can contain submitted values,
  for example the base64 `X-Kafkito-Cluster` header including a password.
  Validation errors are therefore rebuilt from the error structure: the
  location (parameter name or body JSON pointer) plus the violated rule,
  taken only from the schema (type, pattern, bound, required names). A
  test sends invalid requests that carry a password and asserts that
  neither the response nor the log contains it.
- **Two schema validators.** For 3.1 documents kin-openapi validates
  schemas with a JSON Schema 2020-12 validator. Schemas that contain
  `$ref`, such as `ClusterConfig`, fail to compile there, and kin-openapi
  silently falls back to its built-in validator. The fallback also handles
  `type: [T, "null"]` and `const`. The error sanitiser handles both error
  forms and gives the same rule text for both. Tests send real requests
  through the real validator, against the embedded spec and a small
  `$ref`-free 3.1 spec, and pin the exact texts for enum, type, bounds,
  `minItems`, required, `const` and nullable fields. A kin-openapi upgrade
  that changes its messages therefore fails CI.
- **Rules that stay in Go.** Only rules the spec can express live in the
  spec. Blank broker addresses, the SSRF policy and the credentials a
  SASL mechanism needs are checked in `validateClusterPolicy`, for both
  the `_test` body and the `X-Kafkito-Cluster` header. The header is
  decoded by `privateClusterMiddleware` and is not schema-validated.
- **Body limits before body readers.** Every body is capped before
  anything reads it. JSON bodies (create topic, alter configs, delete
  records, create group, reset offsets) are capped at 1 MiB, search at
  1 MiB, register schema at 2 MiB, copy at 32 KiB and the ACL and SCRAM
  user bodies (the ACL delete body included) at 16 KiB, each with its
  previous 400 message. Produce has its own middleware in front of
  the validator. It caps the wire bytes, decompresses a
  `Content-Encoding: gzip` body, caps the decompressed JSON at 15 MiB (413)
  and hands the validator and the handler the plain body. The RBAC
  middleware reads the create-topic and create-group bodies to find the
  topic or group name before any route runs. That read is capped at the
  same 1 MiB, and the bytes it read become the body again for the
  validator and the handler.
- **Streaming (topic copy).** oapi-codegen v2.8.0 streams
  `text/event-stream` responses natively. The generated response writes
  `Content-Type: text/event-stream`, reads the `io.Reader` body in 4 KiB
  chunks, calls `http.Flusher.Flush` after each one and closes the body if
  it is an `io.ReadCloser`. The request log middleware forwards `Flush`.
  `CopyMessages` uses this as follows:
  - Everything that can fail is checked before the stream opens, so the
    failure is still a JSON error with its status. That covers body rules,
    destination resolution, production confirmation, destination RBAC,
    the concurrency slot (429 `copy_concurrency_limit`) and the destination
    topic.
  - The job runs in a goroutine and writes its events into an `io.Pipe`.
    The read side of the pipe is the response body. The event format
    (`data: {json}` plus a blank line) is unchanged, and the response
    wrapper adds `Cache-Control: no-cache` and `X-Accel-Buffering: no`.
    The stream has no `Content-Length`.
  - The job context is `context.WithoutCancel(ctx)` with a 4 h ceiling, so
    the 30 s `middleware.Timeout` deadline does not end a copy.
    `context.AfterFunc` wires only a real cancellation back in, which is
    how net/http reports a client disconnect.
  - After the deadline has fired, a disconnect shows up only as a failed
    write. The generated writer then stops reading and closes the body.
    The body's `Close` cancels the job, and the job's next pipe write
    returns `io.ErrClosedPipe`.
  - The goroutine closes the pipe and releases the concurrency slot in its
    deferred calls, so every way out frees the slot. If the handler
    returns before the goroutine starts, it releases the slot itself.
  - Tests use an `httptest` server and a registry fake that blocks the job.
    They cover the first events arriving while the job still runs, a
    client abort ending the goroutine and freeing the slot, a failed write
    after the deadline stopping the job, the job outliving the deadline,
    the headers, and events matching `CopyProgressEvent`.

## Adding an endpoint

1. Describe the operation in `api/openapi.yaml`: path, `operationId`,
   parameters, request body and every response, with the rules the input
   must meet (enum, pattern, bounds, required, `additionalProperties`).
   Map schemas onto existing Go types in `api/oapi-codegen.overlay.yaml`
   where they exist.
2. Run `make api-generate`. It regenerates `internal/server/api/server.gen.go`
   and `frontend/src/lib/api.gen.ts`.
3. Implement the new `StrictServerInterface` method on `apiServer` in the
   file for its resource (`topics.go`, `groups.go`, ...). Return the
   generated response types, and errors as `apiError` or through
   `upstreamError`/`clusterError`.
4. Mount the generated wrapper method in `internal/server/api_routes.go` on
   the right group, with `noRequestBody` or a body limit
   (`limitRequestBody`/`limitRequestBodyMsg`) in front of `g.validate`. If
   the route needs a permission, add its pattern to `resolvePermission`.
5. Add the operation to `apiOps` in
   `internal/server/generated_routes_test.go` and give it at least one
   successful request in `TestAPIOps_Requests`. The contract, router,
   RBAC, private-cluster and response validation tests then cover it; they
   fail until steps 3–5 are done.

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
- The spec is still hand-written. Every operation is guarded by the
  generated interface, request validation and response validation in the
  contract tests.
- Moving validation into the spec makes some errors stricter or reworded:
  JSON request bodies need `Content-Type: application/json`, and
  validation errors have the code `invalid_request` with generated
  messages. `POST /api/v1/clusters/_test` bodies must use the documented
  lowercase `auth.type` values: `PLAIN` or `" plain "` used to be
  normalised and now returns `400` `invalid_request`. The frontend
  always sends the lowercase values. The `X-Kafkito-Cluster` header is
  unchanged and still accepts `auth.type` case-insensitively and trimmed,
  so private clusters stored by older versions keep working.
- The spec stays the contract. It is not loosened to match lenient
  server behaviour, and `api-breaking` (oasdiff) runs without an ignore
  list.
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
