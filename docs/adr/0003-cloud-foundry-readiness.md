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

## Amendments

### 2026-09-25

Corrections to match the implementation:

1. **Authentication follows the auth-proxy pattern.** Login and sessions are
   handled by an upstream proxy (SAP Approuter on BTP; e.g. oauth2-proxy
   elsewhere). kafkito itself only validates bearer JWTs on `/api/v1/*`,
   selected via `KAFKITO_AUTH_MODE`: `off` (`-tags devauth` builds only),
   `mock`, and `xsuaa` (`-tags btp` builds). A generic `oidc` mode is named in
   config comments but is not registered in code yet. There is
   no PKCE Authorization Code flow in the SPA; it is not implemented and not
   planned. This aligns with the IETF "OAuth 2.0 for Browser-Based Apps"
   recommendation to keep tokens out of the browser via a BFF/proxy.
2. **Port fallback** is `:37421`, not `:8080`. Order: `$PORT`, then
   `server.addr` from config, then `:37421` (`listenAddress` in
   `cmd/kafkito/main.go`).
3. **Reverse-proxy headers:** `chi/middleware.RealIP` is not used;
   `X-Forwarded-*` headers are not interpreted by kafkito.
4. **XSUAA adapter** lives at `internal/auth/xsuaa` and is compiled only with
   the `btp` build tag (see ADR-0004), not at `pkg/auth/xsuaa`.
5. **Local dev auth:** there is no NoOp default. Default builds refuse to start
   with `KAFKITO_AUTH_MODE=off` (the mode default); `off` is only available in
   `-tags devauth` builds, where it injects a synthetic principal, and is
   additionally rejected on Cloud Foundry and on non-loopback binds unless
   `KAFKITO_INSECURE_AUTH_OFF=true`.
6. **VCAP_SERVICES** is read only by `-tags btp` builds, to obtain the XSUAA
   binding. There is no VCAP adapter for Kafka brokers or Schema Registry, and
   the layered config is `defaults → YAML file → environment variables`.
7. **Frontend runtime config:** neither the `/api/v1/config/frontend` endpoint
   nor a `PUBLIC_PATH` base-path setting exists; the SPA is served from `/`.
