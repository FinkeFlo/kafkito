# kafkito e2e harness

Playwright-driven walks against a deterministic local Kafka stack. Runs
opt-in via `make e2e` from the repo root; **not** part of the canonical
hard gate (too slow for per-commit) but expected on the CI workflow for
PR builds.

## Why

QAS — the team's primary live cluster — forbids destructive operations
and rarely lands in the cluster states we need to walk (e.g. an idle
consumer group for Reset-Offsets). This harness gives deterministic
fixture-on-demand for those flows.

## What runs where

```
docker-compose.yml  ← brings up apache/kafka:3.8.1 + cp-schema-registry
                      already in repo, no e2e-specific override
e2e/fixtures/seed.sh  ← seeds the local broker (topic, messages, idle group)
                      uses docker exec into the kafka container
                      kafka-topics.sh / kafka-console-producer.sh
                      / kafka-console-consumer.sh
playwright.config.ts  ← Playwright bootstrap (testDir = ./e2e)
e2e/*.spec.ts         ← the actual walks
make e2e              ← orchestrates: docker up → seed → playwright → down
```

## Prerequisites for local run

- Docker / Docker Compose
- Bun (or compatible Node ≥ 20) for `bunx playwright`
- `playwright install chromium` once (or `playwright install chromium-headless-shell`)

## What is NOT here yet

- ACL grant/revoke walks — needs Keycloak (compose `auth` profile)
- SCRAM rotate walks — same
- Cross-cluster switch walks — needs ≥2 fixture clusters

These are tracked in `kafkito-deploy/specs/2026-05-02-ux-refactor/PLAN.md`
(§ 3.14 "out of scope" list) and can be follow-up tasks.

## Authoring conventions

- Each `.spec.ts` is one walk against one route.
- The walk must be **abort-safe**: never commit a destructive op even
  on the local fixture broker — type the confirm phrase, hit Escape,
  assert focus restoration. The point is to walk the gating UI, not to
  exercise mutation code (we have Go integration tests for that).
- Use the `KAFKITO_E2E_CLUSTER` env var (defaults to `local`) for the
  cluster name in URLs so the fixture name stays in one place.
