# kafkito

<p align="left">
  <img src="./docs/assets/branding/logo.svg" alt="kafkito logo" width="520" />
</p>

> Manage and observe Apache Kafka clusters from a single Go binary — topics, messages, consumer groups, schemas and ACLs in one web UI.

kafkito is a free, open-source web UI for managing and observing Apache Kafka clusters — built in Go as a single binary, with a modern React frontend, Apache 2.0. It's an independent rewrite inspired by [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) (unmaintained since 2024); for a community-maintained continuation of the original Java codebase, see [`kafbat/kafka-ui`](https://github.com/kafbat/kafka-ui).

## Quickstart

> **Run kafkito only behind TLS.** kafkito serves plain HTTP. API requests
> carry bearer tokens and, for private clusters, broker and Schema Registry
> credentials in the `X-Kafkito-Cluster` header. The examples below are for
> your own machine. Anywhere else, terminate TLS in a reverse proxy or
> platform router, set `Strict-Transport-Security` there, and do not expose
> port 37421 directly on an untrusted network. See
> [Running behind TLS](docs/architecture.md#running-behind-tls).

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
  -e KAFKITO_AUTH_MODE=oidc \
  -e KAFKITO_AUTH_OIDC_ISSUER_URL=https://idp.example.com/realms/kafkito \
  -e KAFKITO_AUTH_OIDC_AUDIENCE=kafkito-api \
  -e KAFKITO_KAFKA_BROKERS=host.docker.internal:9092 \
  ghcr.io/finkeflo/kafkito:latest
```

Replace the issuer and audience with your IdP's values; with the example
values discovery fails and the container exits with code 2. The default
image enforces auth and does not start without `KAFKITO_AUTH_MODE`. kafkito has no login flow of its own: put an auth proxy
(for example oauth2-proxy) in front of it that handles the login and
forwards `Authorization: Bearer <access token>`. In mode `oidc` kafkito
checks that token against your OpenID Connect issuer; the signing keys are
found through `<issuer>/.well-known/openid-configuration` unless
`KAFKITO_AUTH_OIDC_JWKS_URL` is set. Mode `mock` checks tokens against a
signing key kafkito creates in memory and never hands out, so every
`/api/v1/*` request gets `401`; use it only to smoke-test the image. See
[Auth modes](https://finkeflo.github.io/kafkito/configuration/#auth-modes).

### SAP BTP / XSUAA

```sh
docker run --rm -p 37421:37421 \
  -e KAFKITO_AUTH_MODE=xsuaa \
  -e VCAP_SERVICES="$VCAP_SERVICES" \
  ghcr.io/finkeflo/kafkito:latest-btp
```

Mode `xsuaa` reads its credentials from an XSUAA service binding in
`VCAP_SERVICES`, which Cloud Foundry sets for a bound app; without it the
image does not start. See [Auth modes](https://finkeflo.github.io/kafkito/configuration/#auth-modes) and
[ADR-0004](./docs/adr/0004-xsuaa-build-tag.md) for the build-tag rationale.

### Build from source

Requires Go 1.26+ and Bun 1.4+:

```sh
git clone https://github.com/FinkeFlo/kafkito && cd kafkito
make build
KAFKITO_AUTH_MODE=mock KAFKITO_KAFKA_BROKERS=localhost:9092 ./bin/kafkito
```

`make build` produces the default build, which needs an auth mode (see
[Auth modes](https://finkeflo.github.io/kafkito/configuration/#auth-modes)). With `mock` the API answers every request with
`401`; for a local UI without an IdP, `make run-dev` runs a `devauth` build
with auth off on `127.0.0.1`.

### Local development

Requires Go 1.26+, Bun 1.4+ and Docker:

```sh
make worktree-init    # once: writes .env.dev with a free port pair
make dev              # Kafka + Schema Registry (Compose), backend and frontend with hot reload
```

Details in [CONTRIBUTING.md](./CONTRIBUTING.md#local-development).

### Configuration

kafkito reads an optional YAML file (`--config` or `KAFKITO_CONFIG`) and
`KAFKITO_*` environment variables, which win over the file. The settings
you need first:

| Variable | Purpose |
|---|---|
| `KAFKITO_AUTH_MODE` | `oidc`, `xsuaa` (`-btp` image), `mock` or `off` (`-local` image only) |
| `KAFKITO_AUTH_OIDC_ISSUER_URL` | OIDC issuer, required for `oidc` |
| `KAFKITO_AUTH_OIDC_AUDIENCE` | Expected token audience, required for `oidc` |
| `KAFKITO_KAFKA_BROKERS` | Brokers of a single cluster named `local`, when no `clusters` are configured |
| `KAFKITO_SERVER_ADDR` / `PORT` | Listen address, default `:37421`; `PORT` wins |
| `KAFKITO_PRIVATE_CLUSTERS` | Who may add private clusters: `on`, `off` or `role` |
| `KAFKITO_LOG_LEVEL` | `debug`, `info`, `warn` or `error` |

Every setting, the auth modes, logging and the security headers are
described in the [Configuration guide](https://finkeflo.github.io/kafkito/configuration/);
data masking in [Data Masking](https://finkeflo.github.io/kafkito/data-masking/).

## Documentation

The full documentation lives at **https://finkeflo.github.io/kafkito/**:
UI guide and workflows, configuration, HTTP API, data masking, architecture
and the design guidelines.

## Why kafkito?

- **Single static binary** — no JVM, no side-car containers, ~50 MB RAM footprint.
- **Graceful with limited permissions** — works with read-only ACLs on individual topics; does not require cluster-admin rights.
- **Built-in RBAC & data masking** — YAML-policy based, OSS, no enterprise gating.
- **Powerful message browser** — JavaScript-DSL filters, JSON/text/binary values, Schema-Registry aware (Avro and JSON Schema decoding; Protobuf payloads are detected but not decoded yet).
- **Cloud-native ready** — stateless, 12-Factor, JWT auth (generic OIDC or XSUAA), distroless image.

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
