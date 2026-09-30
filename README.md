# kafkito

<p align="left">
  <img src="./docs/assets/branding/logo.svg" alt="kafkito logo" width="520" />
</p>

> Manage and observe Apache Kafka clusters from a single Go binary — topics, messages, consumer groups, schemas and ACLs in one web UI.

kafkito is a free, open-source web UI for managing and observing Apache Kafka clusters — built in Go as a single binary, with a modern React frontend, Apache 2.0. It's an independent rewrite inspired by [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) (unmaintained since 2024); for a community-maintained continuation of the original Java codebase, see [`kafbat/kafka-ui`](https://github.com/kafbat/kafka-ui).

## Quickstart

### Try it locally — no auth setup

The `local` image ships with auth disabled and a logged-in dev
identity, so the UI works immediately. Use it to evaluate kafkito
against a local Kafka. **Never deploy this variant.**

```sh
docker run --rm -p 37421:37421 \
  -e KAFKITO_INSECURE_AUTH_OFF=true \
  -e KAFKITO_KAFKA_BROKERS=host.docker.internal:9092 \
  ghcr.io/finkeflo/kafkito:latest-local
```

Auth mode `off` only starts on a loopback address. Inside a container
kafkito listens on all interfaces, so `KAFKITO_INSECURE_AUTH_OFF=true`
acknowledges that; the `-p` mapping above still decides who can reach it.

Open http://localhost:37421 in your browser. Connect kafkito to a
broker on your host (`host.docker.internal:9092`), or run a Kafka
container alongside it on a shared docker network.

### Default image (auth enforced)

```sh
docker run --rm -p 37421:37421 \
  -e KAFKITO_AUTH_MODE=mock \
  -e KAFKITO_KAFKA_BROKERS=host.docker.internal:9092 \
  ghcr.io/finkeflo/kafkito:latest
```

The default image enforces auth and does not start without
`KAFKITO_AUTH_MODE`. The only mode it can serve today is `mock`, which
checks tokens against a signing key kafkito creates in memory at every start
and never hands out. No client can get a token that passes, so every
`/api/v1/*` request is answered with `401`: the UI shell, `/healthz` and
`/readyz` work, the API does not. Use it to smoke-test the image, not to
serve users. For real logins use the `-btp` image with XSUAA; a generic OIDC
mode is in progress (#95). See [Auth modes](#auth-modes).

### SAP BTP / XSUAA

```sh
docker run --rm -p 37421:37421 \
  -e KAFKITO_AUTH_MODE=xsuaa \
  -e VCAP_SERVICES="$VCAP_SERVICES" \
  ghcr.io/finkeflo/kafkito:latest-btp
```

Mode `xsuaa` reads its credentials from an XSUAA service binding in
`VCAP_SERVICES`, which Cloud Foundry sets for a bound app; without it the
image does not start. See [Auth modes](#auth-modes) and
[ADR-0004](./docs/adr/0004-xsuaa-build-tag.md) for the build-tag rationale.

### Build from source

Requires Go 1.26+ and Bun 1.4+:

```sh
git clone https://github.com/FinkeFlo/kafkito && cd kafkito
make build
KAFKITO_AUTH_MODE=mock KAFKITO_KAFKA_BROKERS=localhost:9092 ./bin/kafkito
```

`make build` produces the default build, which needs an auth mode (see
[Auth modes](#auth-modes)). With `mock` the API answers every request with
`401`; for a local UI without an IdP, `make run-dev` runs a `devauth` build
with auth off on `127.0.0.1`.

### Local development (hot-reload)

Requires Go 1.26+, Bun 1.4+, and Docker.

```sh
make worktree-init    # writes .env.dev with a free port pair
make dev              # Compose + backend (air) + frontend (Vite), one command
```

Open the Vite URL printed in the `[frontend]` stream
(default `http://localhost:37422`). Backend changes under `cmd/`,
`internal/`, or `api/` rebuild automatically; frontend changes hot-reload
through Vite. Press Ctrl-C in the `make dev` terminal to stop both
processes (the Compose stack stays up — tear it down with `make dev-down`).
The backend listens on `127.0.0.1` only, because auth mode `off` requires a
loopback address; Vite proxies `/api` to it.

Multiple git worktrees can run `make dev` in parallel; each calls
`make worktree-init` once to claim a free port pair, and they share the
same Compose-backed Kafka and Schema Registry.

From an IDE, running `air` directly works too — `.air.toml` loads
`.env.dev` itself, as long as you've run `make worktree-init` once.

### Configuration

kafkito reads an optional YAML file (`--config` or `KAFKITO_CONFIG`) and
`KAFKITO_*` environment variables. Later sources win: built-in defaults →
YAML file → `KAFKITO_*` variables → `$PORT`.

| Variable                          | YAML key                         | Default   | Notes |
| --------------------------------- | -------------------------------- | --------- | ----- |
| `KAFKITO_SERVER_ADDR`             | `server.addr`                    | `:37421`  | Listen address. |
| `PORT`                            | —                                | —         | If set and non-empty, overrides `server.addr` with `:$PORT` (Cloud Foundry / Heroku). |
| `KAFKITO_TEST_CONNECTION_TIMEOUT` | `server.test_connection_timeout` | `15s`     | Go duration (`30s`, `2m`) for the private-cluster "Test connection" probe. `0` means the default; invalid or negative values fail startup. |
| `KAFKITO_SERVER_FRAME_ANCESTORS`  | `server.frame_ancestors`         | `'none'`  | CSP `frame-ancestors` source list, see [Security headers](#security-headers). |
| `KAFKITO_KAFKA_BROKERS`           | —                                | —         | Env-only shortcut: when no `clusters` are configured, defines one cluster named `local` from a comma-separated broker list. |
| `KAFKITO_AUTH_MODE`               | `auth.mode`                      | `off`     | `mock` (every build), `xsuaa` (`-btp` build) or `off` (served only by `-tags devauth` builds such as the `-local` image). A mode the build cannot serve fails startup, so the default and `-btp` builds need this set. See [Auth modes](#auth-modes). |
| `KAFKITO_INSECURE_AUTH_OFF`       | —                                | —         | `true` allows mode `off` on a non-loopback address. Ignored on Cloud Foundry, where `off` always fails startup. |

An invalid listen address (for example `PORT=abc`) fails startup with exit
code 2.

Per-cluster data masking rules (`clusters[].data_masking`, for record values,
keys and headers) are described in [docs/data-masking.md](docs/data-masking.md).

### Auth modes

`KAFKITO_AUTH_MODE` (YAML `auth.mode`, case-sensitive, default `off`) picks
how kafkito checks the bearer token on `/api/v1/*`; `/healthz` and `/readyz`
are never authenticated. kafkito builds the validator before it listens and
then logs `auth initialised` with the mode. An unknown mode, or one the build
cannot serve, logs `auth init failed` at `error` and exits with code 2. A
generic `oidc` mode does not exist yet (#95).

| Mode    | Builds                          | Startup | IdP outage |
| ------- | ------------------------------- | ------- | ---------- |
| `off`   | served only by `-tags devauth` builds (`-local` image); other builds exit with code 2 | Every request gets the synthetic principal `dev-user`. Exits with code 2 (`insecure auth configuration`) always on Cloud Foundry (`VCAP_APPLICATION` set), and on a non-loopback address unless `KAFKITO_INSECURE_AUTH_OFF=true`. | No IdP involved. |
| `mock`  | every build                     | Starts an issuer on a random `127.0.0.1` port with a signing key created at startup and loads its keys. No client can get a token, so every API request gets `401`. | No external IdP: the issuer runs inside the process. |
| `xsuaa` | `-btp` builds                   | Reads the first `xsuaa` binding from `VCAP_SERVICES`; a missing variable or binding, invalid JSON, or a binding without `url`, `uaadomain` or `xsappname` (or with a scheme or port in `uaadomain`) exits with code 2 (`auth init failed`). Then loads the keys from `<url>/token_keys`, waiting at most 5 s. | At startup: logs `auth: JWKS warm-up failed; startup continues` at `warn` (also when `<url>/token_keys` breaks the `jku` rules below) and serves; API requests get `401` until the keys load, and `/readyz` does not change. Later: requests keep working with the keys already loaded; a new signing key, or a `jku` first seen during the outage, gets `401` until the IdP answers again. |

How `mock` and `xsuaa` load keys:

- One load per key URL runs at a time and all requests share it. A request
  waits at most 5 s for it and then gets `401`; the load itself may take up
  to 10 s, so a slow IdP still fills the cache for later requests.
- A load starts at most once per minute per URL: for the first keys, when a
  token names a `kid` the cached keys lack (key rotation), or in the
  background on the first request after the keys are 15 min old. Once that
  load has ended, requests in the rest of the minute do not wait: without
  keys they get `401` at once, and a token with an unknown `kid` is checked
  against the cached keys and rejected.
- Nothing retries on a timer: after a failed start the next load begins with
  the first API request at least a minute later.
- A failed load logs `jwks fetch failed` at `warn` and keeps the previous
  keys. The URL is logged for the configured key URL only; key URLs taken
  from a token (`xsuaa`) are not.
- A rejected token is logged only at `debug` (`auth validate failed`, with
  the rule that failed and no claim values); the `401` itself appears in the
  request log at `info`.

Tokens (`mock` and `xsuaa`) must meet all of these:

- A compact JWS with exactly one signature, a `kid` header and a JSON claim
  set as payload.
- Header `alg` is one of `RS256`, `RS384`, `RS512`, `PS256`, `PS384`,
  `PS512`, `ES256`, `ES384`, `ES512` or `EdDSA` (HMAC and `none` are always
  rejected) and equals the key's `alg` when the key has one.
- `exp` is present and not past; `nbf` and `iat`, when present, are checked
  too, with 60 s clock skew allowed.
- `sub` is a non-empty string and `aud` is present.
- `mock`: `iss` equals the embedded issuer URL exactly, `aud` contains
  `mock-client`.
- `xsuaa`: `iss` is the binding `url` or a path below it, `aud` contains the
  binding `clientid` or `xsappname`, `zid` equals the binding
  `identityzoneid` when that is set, and the `jku` header is
  `https://<uaadomain or a subdomain>/token_keys` (a DNS name, no port other
  than 443, no user info, query or fragment). At most 16 different `jku`
  URLs, the binding's own included, are accepted per process; tokens naming
  further ones get `401`.

### Logging

kafkito logs to stdout via `log/slog`, one line per event:

| Variable             | Values                           | Default |
| -------------------- | -------------------------------- | ------- |
| `KAFKITO_LOG_LEVEL`  | `debug`, `info`, `warn`, `error` | `info`  |
| `KAFKITO_LOG_FORMAT` | `json`, `text`                   | `json`  |

The YAML equivalents are `log.level` and `log.format`; `make dev` defaults to
`text`. Every API request gets a request id (from `X-Vcap-Request-Id`,
`traceparent` or `X-Request-Id`, else generated), echoed in the
`X-Request-Id` response header and attached to its log lines. Server errors
(5xx) log at `warn`, client errors (4xx) and requests slower than 2s at
`info`, all other requests at `debug`; health probes and static assets are not
logged, and query strings, headers and bodies never are.

To see every request on SAP BTP Cloud Foundry temporarily:

```sh
cf set-env <app> KAFKITO_LOG_LEVEL=debug && cf restart <app>
```

### Security headers

Every response (API, UI and static files) carries:

```text
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cross-Origin-Opener-Policy: same-origin
X-Frame-Options: DENY
```

Private-cluster credentials are kept in the browser's localStorage, so the
policy allows no inline scripts or styles and no third-party origins.

- **Embedding in an iframe** (e.g. an SAP BTP launchpad): set
  `KAFKITO_SERVER_FRAME_ANCESTORS` (YAML `server.frame_ancestors`) to a
  space-separated CSP source list, e.g. `'self' https://*.launchpad.example.com`.
  `X-Frame-Options: DENY` is only sent while the value is `'none'`, because it
  cannot express an allow-list.
- **HSTS** is not set by kafkito: TLS is terminated by the upstream proxy or
  router, which should send `Strict-Transport-Security`.
- **`make dev`** serves the UI through Vite, which sets none of these headers;
  the policy only applies when the Go binary serves the UI (`make build`,
  images, `make e2e`).

## Why kafkito?

- **Single static binary** — no JVM, no side-car containers, ~50 MB RAM footprint.
- **Graceful with limited permissions** — works with read-only ACLs on individual topics; does not require cluster-admin rights.
- **Built-in RBAC & data masking** — YAML-policy based, OSS, no enterprise gating.
- **Powerful message browser** — JavaScript-DSL filters, JSON/text/binary values, Schema-Registry aware (Avro and JSON Schema decoding; Protobuf payloads are detected but not decoded yet).
- **Cloud-native ready** — stateless, 12-Factor, JWT auth (XSUAA build), distroless image.

## Tech Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.26 · Chi · OpenAPI 3.1 (`oapi-codegen`, `kin-openapi`) · `twmb/franz-go` + `kadm` · `dop251/goja` · `knadh/koanf` · `log/slog` |
| Frontend | React 19 · Vite · TanStack Router · TanStack Query · Tailwind · Radix UI primitives · Bun |
| Distribution | Single Go binary (`//go:embed`-ed SPA) · distroless multi-arch Docker image |

## Project Status

See [docs/adr/](./docs/adr/) for Architecture Decision Records.

- [x] ADR-0001: Greenfield Apache-2.0
- [x] ADR-0002: Tech Stack
- [x] ADR-0003: Cloud Foundry Readiness
- [x] ADR-0004: XSUAA as a build-tagged plugin
- [x] ADR-0005: OpenAPI 3.1 as the HTTP contract

## Contributing

Pull requests are welcome. See [CONTRIBUTING.md](./CONTRIBUTING.md) for the
DCO sign-off requirement, local checks, and style rules.

## License

Apache License 2.0 — see [LICENSE](./LICENSE) and [NOTICE](./NOTICE).

## Acknowledgements

- [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) — original Apache-2.0 reference for features (RBAC, masking, graceful degradation). Unmaintained since 2024. We may port code from there with attribution.
- [`kafbat/kafka-ui`](https://github.com/kafbat/kafka-ui) — community-maintained continuation of `provectus/kafka-ui` (Apache-2.0, Java/Spring). Different stack, shared goals. Worth a look if you want a maintained fork of the original codebase.
