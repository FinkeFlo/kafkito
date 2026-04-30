# ADR-0003: Cloud Foundry readiness

- **Status:** Accepted
- **Date:** 2026-04-22

## Context

kafkito must be deployable to Cloud Foundry (CF) — including SAP BTP CF — as well as Kubernetes, ECS, and other container platforms. Cloud Foundry imposes specific conventions (12-factor, `$PORT` binding, stateless processes, `VCAP_SERVICES` bindings, JWT-based auth with XSUAA). Designing for CF from the outset costs very little and aligns with general cloud-native best practices.

## Decision

kafkito will adopt the following principles from day one. None are CF-exclusive — they are 12-factor hygiene.

### Runtime

1. **Bind to `$PORT`** (fall back to `:8080` for local dev). Implemented in `cmd/kafkito/main.go`.
2. **Stateless process.** No local filesystem writes that the app depends on; no in-memory sessions that assume sticky routing. All persistent state goes to explicit external services (later phase: optional Postgres metastore).
3. **Logs to stdout/stderr** in JSON (`slog`). No local log files.
4. **Graceful shutdown on SIGTERM** within 10 seconds. Already wired in `main.go`.
5. **Trust reverse-proxy headers** (`X-Forwarded-*`) via `chi/middleware.RealIP`, because the CF router terminates TLS.
6. **Health endpoints** `/healthz` (liveness) and `/readyz` (readiness). Readiness will flip to `false` until Kafka connectivity is verified (added in later phase).

### Configuration

- **Layered config via `koanf`:** `defaults → YAML file → environment variables → VCAP_SERVICES`.
- **VCAP adapter** (later phase) parses `VCAP_SERVICES` for Kafka broker credentials, Schema Registry URLs, OIDC issuer metadata.
- **No build-time baked secrets or URLs.** One image must be promotable across all environments.

### Authentication

- **Primary path: generic OIDC** (Discovery URL + RS256 JWTs). Compatible with XSUAA, Keycloak, Azure AD, Google, Okta.
- **Stateless JWT validation** — no server-side session store required.
- **PKCE Authorization Code flow** for the SPA; access tokens sent as Bearer headers to the backend.
- **XSUAA-specific mapping** (role collections → kafkito roles) will live in an optional adapter (`pkg/auth/xsuaa`), not the core auth code.
- **Local dev**: NoOp auth (no login) by default, with an optional `docker compose --profile auth` Keycloak stack for integration testing.

### Frontend

- SPA served statically by the Go backend via `//go:embed`. No separate frontend container.
- **All API paths relative** (`/api/v1/...`) — never hard-code a host.
- **Runtime config endpoint** `/api/v1/config/frontend` delivers env-specific settings (OIDC issuer, feature flags) at boot. No compile-time config baking.
- **Configurable base path** (`PUBLIC_PATH`) to support mounting kafkito at a non-root URL.

### Packaging

- **Distroless, multi-arch (`linux/amd64`, `linux/arm64`) Docker image** pushed via GitHub Actions.
- **`cf push` compatible** through the Docker deployment method (CF supports Docker images directly; we do not rely on a specific buildpack).

## Consequences

**Positive**

- Cloud Foundry deployment is a non-event: `cf push kafkito --docker-image ghcr.io/finkeflo/kafkito:<tag>` plus a `manifest.yml` binding Kafka and OIDC services.
- Same image and binary run locally, in K8s, on ECS, or on Fly.io with no changes.
- Auth strategy survives both air-gapped self-hosted (Keycloak) and public-cloud (XSUAA, Azure AD) deployments.

**Negative**

- We incur early plumbing (healthz, graceful shutdown, layered config) before building Kafka features. This is cheap and non-controversial.
- Dual support (local NoOp vs. OIDC) creates two code paths we must test.

## Alternatives considered

- **Assume Kubernetes only.** Rejected — CF compatibility costs almost nothing and is explicitly required by our target users.
- **Server-side sessions with Redis.** Rejected — a stateless JWT approach avoids an extra dependency and works on any platform.
- **Use CF-specific libraries (e.g. `cfenv`).** Rejected — we prefer a thin koanf adapter over vendor lock-in.
