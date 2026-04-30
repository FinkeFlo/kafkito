# ADR-0001: Greenfield Go implementation under Apache 2.0

- **Status:** Accepted
- **Date:** 2026-04-22

## Context

We set out to build a modern successor to [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui), which has been stagnating since 2024. Two candidate starting points were considered:

1. **Hard-fork of [`redpanda-data/console`](https://github.com/redpanda-data/console)** — a mature Go codebase covering ~80 % of the feature scope, with an active upstream.
2. **Greenfield Go implementation** — clean-room, same architectural stack, Apache-2.0 from day one.

### License analysis of redpanda-data/console

An audit of the repository (master, commit `534690f`) showed that the entire first-party codebase — ~840 Go and TypeScript files — is licensed under the **Business Source License 1.1 (BSL-1.1)**:

```
// Copyright 2022 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file https://github.com/redpanda-data/redpanda/blob/dev/licenses/bsl.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0
```

Key implications of the BSL-1.1 (from `redpanda-data/redpanda/licenses/bsl.md`):

- Self-hosting (including commercial) is allowed.
- **Offering the software as a managed Streaming/Queueing service to third parties is prohibited.**
- Not OSI-approved. Incompatible with policies that mandate OSI-approved OSS only.
- Derivative works inherit BSL; the per-file header cannot be removed.
- Automatic conversion to Apache-2.0 four years after each version's release (Change Date).

Mixing or rewriting files does not "dilute" the BSL — the entire distribution remains BSL-bound as long as a single BSL file is present.

The only Apache-2.0 files in `redpanda-data/console` are generated Google protobuf stubs (`frontend/src/protogen/google/api/*`), which are third-party code retaining their upstream license.

## Decision

We will build kafkito as a **Greenfield Go implementation, licensed under Apache License 2.0**. We will **not** fork or copy source code from `redpanda-data/console`.

- Architectural inspiration from Redpanda Console is permitted (ideas and APIs are not copyrightable; specific expression is).
- Feature and code portability from [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui) (Apache-2.0) is explicitly allowed with attribution.
- The technology stack deliberately mirrors Redpanda Console's choices (Chi, Connect-RPC, franz-go, goja, React + Bun) because these are independent, well-known open-source libraries — using them does not make our code derivative.

## Consequences

**Positive**

- kafkito is OSI-approved Apache-2.0. Acceptable in all corporate compliance contexts.
- Freedom to offer kafkito in any deployment model, including managed services.
- No lingering BSL obligations or attribution burdens.
- Clean, modern codebase without legacy trade-offs.

**Negative**

- Significantly larger initial implementation effort — estimated 12–18 months to feature parity with Redpanda Console.
- We give up ~80 % of ready-to-ship code that the fork approach would have provided.
- Risk of re-inventing subtle details (e.g. Avro/Protobuf deserialization edge cases) that Redpanda Console already handles well.

**Mitigation**

- Aggressively port features from `provectus/kafka-ui` (Apache-2.0) where code reuse is lawful.
- Ship a deliberately scoped MVP first (topic list + read-only message browser) and iterate.
- Use battle-tested upstream libraries (franz-go, kadm, sr) rather than implementing Kafka primitives ourselves.

## Alternatives considered

- **Fork pre-BSL "Kowl" history (pre-2022 Apache-2.0).** Rejected: the pre-BSL codebase is ~4 years stale; nearly all valuable features were added under BSL.
- **Fork AKHQ (Apache-2.0, Java/Micronaut).** Rejected: we want a Go single-binary stack.
- **Accept BSL and fork `redpanda-data/console`.** Rejected: conflicts with stated goal of OSI-approved, managed-service-friendly licensing.
