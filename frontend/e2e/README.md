# kafkito e2e harness

Playwright-driven walks against a deterministic local Kafka stack. Runs
opt-in via `make e2e` from the repo root; **not** part of the canonical
hard gate (too slow for per-commit) but expected on the CI workflow for
PR builds (`.github/workflows/e2e.yml`).

## Why

QAS — the team's primary live cluster — forbids destructive operations
and rarely lands in the cluster states we need to walk (e.g. an idle
consumer group for Reset-Offsets). This harness gives deterministic
fixture-on-demand for those flows.

## Topology — hermetic by design

`make e2e` does NOT collide with a running `make dev` stack. It uses:

```
Kafka broker      docker compose ↑ kafkito-kafka  : 39092 (host)  ← seed.sh writes here
Schema Registry   not started for e2e (not needed by current walks)
kafkito (Go)      subprocess on PORT=47421       : 47421 (host)  ← Playwright targets here
                  built with -tags devauth so KAFKITO_AUTH_MODE=off is allowed
                  serves the embedded frontend; Vite is NOT involved
```

`make dev` keeps using `:37421` (kafkito) and `:37422` (Vite). Both can
coexist with `make e2e` because the e2e kafkito binds a different port
and uses the fresh local Kafka cluster (auto-named `local`).

## Quickstart (local)

```bash
# one-time
cd frontend && bunx playwright install chromium

# every run
make e2e             # = make e2e-up e2e-test e2e-down
```

If the run fails:
- Logs from the e2e kafkito process: `/tmp/kafkito-e2e.log`
- Playwright HTML report: `frontend/playwright-report/index.html`
- Traces / videos / screenshots on failed tests: `frontend/test-results/`

To override the port: `make e2e E2E_PORT=47431`.

## File map

```
docker-compose.yml                  apache/kafka:3.8.1 + cp-schema-registry (existing)
frontend/playwright.config.ts       Playwright bootstrap (testDir = ./e2e)
frontend/e2e/fixtures/seed.sh       seeds the broker via `docker exec kafkito-kafka`
frontend/e2e/*.spec.ts              the actual walks
Makefile :: e2e, e2e-up, e2e-test, e2e-down
.github/workflows/e2e.yml           CI workflow with browser caching + artifact upload
```

## Authoring conventions

- Each `.spec.ts` is one walk against one route.
- The walk must be **abort-safe**: never commit a destructive op even on
  the local fixture broker — type the confirm phrase, hit Escape, assert
  focus restoration. The point is to walk the gating UI, not to exercise
  mutation code (we have Go integration tests for that).
- Cluster name in URLs is `KAFKITO_E2E_CLUSTER` (defaults to `local` —
  the auto-cluster name from the `KAFKITO_KAFKA_BROKERS` shortcut).

## What is NOT here yet

Out of scope for the current iteration; tracked in
`kafkito-deploy/specs/2026-05-02-ux-refactor/PLAN.md` § 3.14 follow-ups:

- ACL grant/revoke walks — needs Keycloak (compose `auth` profile)
- SCRAM rotate walks — same
- Cross-cluster switch walks — needs ≥2 fixture clusters
