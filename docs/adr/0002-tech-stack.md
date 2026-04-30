# ADR-0002: Technology stack

- **Status:** Accepted
- **Date:** 2026-04-22

## Context

kafkito is a single-binary Kafka management UI (see ADR-0001). We need a stack that:

- Produces a small, fast, statically linked binary.
- Provides first-class, actively maintained Kafka client support.
- Enables a protobuf-first API layer suitable for both backend and web/mobile clients.
- Ships a modern, maintainable SPA frontend that we can embed via `//go:embed`.
- Is Cloud Foundry / Kubernetes / container-friendly out of the box (see ADR-0003).

## Decision

### Backend

| Concern | Choice | Rationale |
|---|---|---|
| Language | **Go 1.26+** | Single-binary, static linking, strong stdlib, mature Kafka client ecosystem. |
| HTTP router | **`go-chi/chi` v5** | Idiomatic, stdlib-compatible, excellent middleware story. |
| RPC layer | **Connect-RPC with `buf`** (protobuf-first) | HTTP/1.1, HTTP/2, gRPC-compatible, generated TypeScript client, API contract versioning. |
| Kafka client | **`twmb/franz-go`** + **`kadm`** + **`kmsg`** + **`sr`** | Best-of-breed pure-Go Kafka client; supports KIPs promptly; wire-protocol-first. |
| Scripting | **`dop251/goja`** | Sandboxed JS engine for message-filter DSL. |
| Config | **`knadh/koanf`** | YAML + env + VCAP_SERVICES layering. |
| Logging | **`log/slog`** (stdlib) with optional `go.uber.org/zap` handler later if perf demands it | Structured JSON logs to stdout, Cloud Foundry loggregator-friendly. |
| Observability | **OpenTelemetry** + **Prometheus** exporter | Industry standard. |

### Frontend

| Concern | Choice | Rationale |
|---|---|---|
| Runtime | **Bun** | Fast install/build, first-class TypeScript, matches modern React tooling. |
| Framework | **React 19** | Mainstream, large talent pool, stable ecosystem. |
| Build | **Vite** | Fast dev server, lean production bundles. |
| Routing | **TanStack Router** | Type-safe, data-loading built in, no server-side rendering required. |
| UI kit | **shadcn/ui** + **Tailwind CSS** | Copy-in component library (no vendor lock-in), accessible defaults. |
| State / data | **TanStack Query** + Connect-RPC generated client | Declarative data-fetching; protobuf types end-to-end. |

### Distribution

- **Single Go binary** with the SPA embedded via `//go:embed`.
- **Distroless multi-arch container image** (`gcr.io/distroless/static-debian12`, `linux/amd64` + `linux/arm64`).

## Consequences

**Positive**

- One deployment artifact. No JVM, no sidecar node_modules.
- Protobuf-first API ensures end-to-end type safety and easy contract evolution.
- Stack aligns with Redpanda Console's architectural choices, which are battle-tested — but we implement clean-room (see ADR-0001).

**Negative**

- Bun is less mainstream in some enterprise environments than Node.js. We mitigate by producing plain static assets — the runtime is only a build-time dependency.
- Connect-RPC is newer than plain REST + OpenAPI. Team members need to learn `buf` and the Connect generator.

## Alternatives considered

- **Echo / Gin** instead of Chi — rejected, Chi has the cleanest middleware story and is stdlib-compatible.
- **Sarama** or **IBM/sarama** instead of franz-go — rejected, franz-go is more modern, has better performance and KIP coverage.
- **Next.js** or **Remix** instead of Vite SPA — rejected, we do not need SSR; a SPA simplifies the `//go:embed` distribution model.
- **gRPC-Web / grpc-gateway** instead of Connect-RPC — rejected, Connect-RPC offers a simpler protocol with identical tooling benefits.
