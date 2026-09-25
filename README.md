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
  -e KAFKITO_KAFKA_BROKERS=host.docker.internal:9092 \
  ghcr.io/finkeflo/kafkito:latest-local
```

Open http://localhost:37421 in your browser. Connect kafkito to a
broker on your host (`host.docker.internal:9092`), or run a Kafka
container alongside it on a shared docker network.

### Production image (OIDC / JWT)

```sh
docker run --rm -p 37421:37421 \
  -e KAFKITO_AUTH_MODE=mock \
  -e KAFKITO_KAFKA_BROKERS=host.docker.internal:9092 \
  ghcr.io/finkeflo/kafkito:latest
```

The default image enforces auth. Use `KAFKITO_AUTH_MODE=mock` for
JWT-validation testing, or wire in your own OIDC issuer for
real-world deploys.

### SAP BTP / XSUAA

```sh
docker run --rm -p 37421:37421 ghcr.io/finkeflo/kafkito:latest-btp
```

See [ADR-0004](./docs/adr/0004-xsuaa-build-tag.md) for the
build-tag rationale.

### Build from source

Requires Go 1.26+ and Bun 1.4+:

```sh
git clone https://github.com/FinkeFlo/kafkito && cd kafkito
make build
KAFKITO_KAFKA_BROKERS=localhost:9092 ./bin/kafkito
```

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

An invalid listen address (for example `PORT=abc`) fails startup with exit
code 2.

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
- **Powerful message browser** — JavaScript-DSL filters, Avro/Protobuf/JSON/Text encodings, Schema-Registry aware.
- **Cloud-native ready** — stateless, 12-Factor, OIDC/JWT auth, distroless image.

## Tech Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.26 · Chi · OpenAPI 3.1 · `twmb/franz-go` + `kadm` + `sr` · `dop251/goja` · `knadh/koanf` · `log/slog` |
| Frontend | React 19 · Vite · TanStack Router · shadcn/ui · Tailwind · Bun |
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
