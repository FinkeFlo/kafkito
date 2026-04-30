# kafkito

> A modern, lightweight, single-binary UI for Apache Kafka.

**Status:** Pre-alpha (v0.0.x). APIs and on-disk formats may change without notice.

kafkito is the spiritual successor to [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) — a free, open-source web UI for managing and observing Apache Kafka clusters. Built in Go with a modern React frontend, shipped as a single binary, licensed under Apache 2.0.

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

Requires Go 1.26+ and Bun 1.3+:

```sh
git clone https://github.com/FinkeFlo/kafkito && cd kafkito
make build
KAFKITO_KAFKA_BROKERS=localhost:9092 ./bin/kafkito
```

## Why kafkito?

- **Single static binary** — no JVM, no side-car containers, ~50 MB RAM footprint.
- **Graceful with limited permissions** — works with read-only ACLs on individual topics; does not require cluster-admin rights.
- **Built-in RBAC & data masking** — YAML-policy based, OSS, no enterprise gating.
- **Powerful message browser** — JavaScript-DSL filters, Avro/Protobuf/JSON/Text encodings, Schema-Registry aware.
- **Cloud-native ready** — stateless, 12-Factor, OIDC/JWT auth, distroless image.

## Tech Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.26 · Chi · Connect-RPC (`buf`) · `twmb/franz-go` + `kadm` + `sr` · `dop251/goja` · `knadh/koanf` · `zap`/`slog` · OpenTelemetry |
| Frontend | React 19 · Vite · TanStack Router · shadcn/ui · Tailwind · Bun |
| Distribution | Single Go binary (`//go:embed`-ed SPA) · distroless multi-arch Docker image |

## Project Status

See [docs/adr/](./docs/adr/) for Architecture Decision Records.

- [x] ADR-0001: Greenfield Apache-2.0 (not a fork of redpanda-data/console)
- [x] ADR-0002: Tech Stack
- [x] ADR-0003: Cloud Foundry Readiness
- [x] ADR-0004: XSUAA as a build-tagged plugin
- [ ] v0.1.0 MVP: topic list + read-only message browser

## Contributing

Pull requests are welcome. See [CONTRIBUTING.md](./CONTRIBUTING.md) for the
DCO sign-off requirement, local checks, and style rules.

## License

Apache License 2.0 — see [LICENSE](./LICENSE) and [NOTICE](./NOTICE).

## Acknowledgements

- [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) — Apache-2.0 reference for features (RBAC, masking, graceful degradation). We may port code from there with attribution.
- [`redpanda-data/console`](https://github.com/redpanda-data/console) — BSL-1.1 architectural inspiration only. **No source code copied.**
