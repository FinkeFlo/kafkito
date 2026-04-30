# kafkito

> A modern, lightweight, single-binary UI for Apache Kafka.

**Status:** Pre-alpha (v0.0.x). APIs and on-disk formats may change without notice.

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

Requires Go 1.26+ and Bun 1.3+:

```sh
git clone https://github.com/FinkeFlo/kafkito && cd kafkito
make build
KAFKITO_KAFKA_BROKERS=localhost:9092 ./bin/kafkito
```

### Local development (hot-reload)

Requires Go 1.26+, Bun 1.3+, and Docker.

```sh
make worktree-init    # writes .env.dev with a free port pair
make dev              # Compose + backend (air) + frontend (Vite), one command
```

Open the Vite URL printed in the `[frontend]` stream
(default `http://localhost:37422`). Backend changes under `cmd/`,
`internal/`, or `pkg/` rebuild automatically; frontend changes hot-reload
through Vite. Press Ctrl-C in the `make dev` terminal to stop both
processes (the Compose stack stays up — tear it down with `make dev-down`).

Multiple git worktrees can run `make dev` in parallel; each calls
`make worktree-init` once to claim a free port pair, and they share the
same Compose-backed Kafka and Schema Registry.

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

- [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) — original Apache-2.0 reference for features (RBAC, masking, graceful degradation). Unmaintained since 2024. We may port code from there with attribution.
- [`kafbat/kafka-ui`](https://github.com/kafbat/kafka-ui) — community-maintained continuation of `provectus/kafka-ui` (Apache-2.0, Java/Spring). Different stack, shared goals. Worth a look if you want a maintained fork of the original codebase.
- [`redpanda-data/console`](https://github.com/redpanda-data/console) — BSL-1.1 architectural inspiration only. **No source code copied.**
