---
name: api-endpoint
description: Add or change an HTTP API operation the spec-first way of ADR-0005: api/openapi.yaml, make api-generate, strict handler, route mount, RBAC permission, apiOps test entry, typed frontend client and query factory. Use when a task touches api/openapi.yaml, adds an endpoint, changes a request or response shape, or needs a new call from the frontend.
---

# Add or change an API operation

Read `docs/adr/0005-openapi-contract.md` ("Adding an endpoint") once. Never
edit `internal/server/api/server.gen.go` or `frontend/src/lib/api.gen.ts`.

1. **Spec.** Describe the operation in `api/openapi.yaml`: `operationId`,
   parameters, request body, every response (reuse the `Error` schema) and
   the input rules the request must meet (enum, pattern, min/max, required,
   `additionalProperties`). Rules live in the spec, not in Go. Map a schema
   onto an existing Go type in `api/oapi-codegen.overlay.yaml` when one exists.
   Removing or narrowing anything fails the `api-breaking` job (oasdiff,
   `fail-on: ERR`): prefer additive changes and name intentional breaks in the PR.
2. **Generate.** `make api-generate` (also run by `make api-check`).
3. **Handler.** Implement the new `StrictServerInterface` method on
   `apiServer` in `internal/server/<resource>.go` (`topics.go`, `groups.go`,
   `schemas.go`, `acls.go`, `scram_users.go`, `messages.go`, `clusters_api.go`,
   `system.go`). Return the generated response types; return errors as
   `apiError` (`internal/server/apierror.go`) or through the existing upstream
   and cluster error helpers used by the neighbouring handlers. Talk to Kafka
   only through the store interfaces in `internal/server/stores.go`; a new
   Kafka operation goes on its service in `internal/kafka` and on the matching
   store interface. Error messages never repeat a submitted value.
4. **Mount.** Register the generated wrapper method in
   `internal/server/api_routes.go` on the right group (`mountRoot` for
   probes, `mountMeta` for authenticated meta endpoints, `mountClusters` for
   cluster routes) with `noRequestBody` or `limitRequestBody`/
   `limitRequestBodyMsg` in front of `g.validate`. Add the route pattern to
   `resolvePermission` in `internal/server/rbac.go`, or to `rbacExemptRoutes`
   with a reason.
5. **Tests.** Add the operation to `apiOps` in
   `internal/server/generated_routes_test.go` (id, method, pattern, group,
   resource and action) and at least one successful request in
   `TestAPIOps_Requests`. Add a handler test with the fakes in
   `internal/server/fakes_test.go` / `fakebroker_test.go`. Run
   `go test -race -count=1 ./internal/server/...` and `make api-check`.
6. **Frontend.** Add the endpoint function to `frontend/src/lib/api.ts`
   (typed `openapi-fetch` client in `api-client.ts`; export the response type
   as an alias of the generated schema). Reads get a `queryOptions()` factory
   in `frontend/src/lib/queries/<resource>.ts` keyed with
   `clusterKey(cluster)`; writes use `useMutation` and invalidate the
   factory keys. `cd frontend && bun run lint && bun run test`.
7. **Docs.** `docs/API.md` for user-facing operations; the README
   configuration table for a new setting.

Finish with `make check`, and run the `api-contract-reviewer` subagent on the
diff.
