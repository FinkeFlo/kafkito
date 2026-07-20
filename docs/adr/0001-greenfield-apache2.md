# ADR-0001: Greenfield Go implementation under Apache 2.0

- **Status:** Accepted
- **Date:** 2026-04-22

## Context

We set out to build a modern Kafka management UI in the lineage of
[`provectus/kafka-ui`](https://github.com/provectus/kafka-ui), which has
been stagnating since 2024. The community-maintained continuation of
that codebase is [`kafbat/kafka-ui`](https://github.com/kafbat/kafka-ui)
(Apache-2.0, Java/Spring). For a Go single-binary stack, two candidate
starting points were considered:

1. **Forking an existing BSL-licensed Go codebase** — lower initial
   implementation effort.
2. **Greenfield Go implementation** — clean-room, Apache-2.0 from day one.

### License analysis of the fork option

Key implications of BSL-1.1:

- Self-hosting (including commercial) is allowed.
- **Offering the software as a managed Streaming/Queueing service to third parties is prohibited.**
- Not OSI-approved. Incompatible with policies that mandate OSI-approved OSS only.
- Derivative works inherit BSL; the per-file header cannot be removed.
- Automatic conversion to Apache-2.0 four years after each version's release (Change Date).

Mixing or rewriting files does not "dilute" the BSL — the entire distribution remains BSL-bound as long as a single BSL file is present.

## Decision

We will build kafkito as a **Greenfield Go implementation, licensed
under Apache License 2.0**. We will **not** fork or copy source code
from BSL-licensed codebases.

- Feature and code portability from
  [`provectus/kafka-ui`](https://github.com/provectus/kafka-ui)
  (Apache-2.0) is explicitly allowed with attribution.

## Consequences

**Positive**

- kafkito is OSI-approved Apache-2.0. Acceptable in all corporate compliance contexts.
- Freedom to offer kafkito in any deployment model, including managed services.
- No lingering BSL obligations or attribution burdens.
- Clean, modern codebase without legacy trade-offs.

**Negative**

- Significantly larger initial implementation effort than a direct fork.
- We give up substantial ready-to-ship code that a fork approach would
  have provided.
- Risk of re-inventing subtle details (for example Avro/Protobuf
  deserialization edge cases) that mature UIs already handle well.

**Mitigation**

- Aggressively port features from `provectus/kafka-ui` (Apache-2.0) where code reuse is lawful.
- Ship a deliberately scoped MVP first (topic list + read-only message browser) and iterate.
- Use battle-tested upstream libraries (franz-go, kadm, sr) rather than implementing Kafka primitives ourselves.

## Alternatives considered

- **Fork pre-BSL "Kowl" history (pre-2022 Apache-2.0).** Rejected:
  the pre-BSL codebase is ~4 years stale; nearly all valuable features
  were added under BSL.
- **Fork [`kafbat/kafka-ui`](https://github.com/kafbat/kafka-ui) (Apache-2.0, community continuation of provectus/kafka-ui).** Rejected: Java/Spring stack conflicts with the goal of a Go single-binary distribution.
- **Fork AKHQ (Apache-2.0, Java/Micronaut).** Rejected: we want a Go single-binary stack.
- **Accept BSL and fork a BSL-licensed Go UI codebase.** Rejected:
  conflicts with stated goal of OSI-approved, managed-service-friendly
  licensing.
