# kafkito e2e harness

Playwright-driven walks against a deterministic local Kafka stack. Runs
opt-in via `make e2e` from the repo root; **not** part of the canonical
hard gate (too slow for per-commit) but expected on the CI workflow for
PR builds (`.github/workflows/e2e.yml`).

## Topology — hermetic by design

`make e2e` does NOT collide with a running `make dev` stack. It uses:

```
Kafka broker      docker compose ↑ kafkito-kafka  : 39092 (host)  ← seed.sh writes here
                  runs the StandardAuthorizer with User:ANONYMOUS as super
                  user, so the ACL walk can create and delete rules
Schema Registry   docker compose ↑ kafkito-schema-registry : 38081 (host)  ← seed.sh registers subjects here
                  confluentinc/cp-schema-registry:7.6.2, stores its schemas in
                  the fixture broker's _schemas topic
kafkito (Go)      subprocess on PORT=47421       : 47421 (host)  ← Playwright targets here
                  built with -tags devauth so KAFKITO_AUTH_MODE=off is allowed
                  serves the embedded frontend; Vite is NOT involved
                  configured by fixtures/kafkito-e2e.yaml (KAFKITO_CONFIG)
```

`make e2e-up` starts both containers with `docker compose up -d --wait kafka
schema-registry`, so it returns once both healthchecks pass. `make
e2e-down` (and `make e2e-clean`) stop the kafkito binary and the Schema
Registry container, but only the container if `make e2e-up` started it:
a registry that a running `make dev` stack already owns keeps running.
Kafka is left running, as before.

`make dev` keeps using `:37421` (kafkito) and `:37422` (Vite). Both can
coexist with `make e2e` because the e2e kafkito binds a different port
and uses the fresh local Kafka cluster (named `local` in
`fixtures/kafkito-e2e.yaml`, which also holds the Schema Registry URL and
the `data_masking` rules the masking walk needs). Both stacks share the
same Kafka and Schema Registry containers; `seed.sh` only touches the
`e2e-*` topics and subjects.

## Quickstart (local)

Prerequisites: Docker (with Compose), Bun, Go, and `curl` and `jq` on your
PATH. `seed.sh` calls the Schema Registry REST API with curl and jq and
exits early with a hint if one of them is missing (macOS: `brew install
jq`; Debian/Ubuntu: `sudo apt-get install -y jq curl`; the GitHub-hosted
Ubuntu runners have both).

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
docker-compose.yml                  apache/kafka:3.8.1 + cp-schema-registry:7.6.2
frontend/playwright.config.ts       Playwright bootstrap (testDir = ./e2e)
frontend/e2e/fixtures/seed.sh       seeds the broker via `docker exec kafkito-kafka` and the Schema
                                    Registry via its REST API (Avro subject with two versions, JSON
                                    Schema and Protobuf subjects, and the e2e-avro-orders topic with
                                    Avro records in the Confluent wire format)
frontend/e2e/fixtures/kafkito-e2e.yaml  kafkito config: cluster `local` + its Schema Registry URL +
                                    data_masking for e2e-masked(-kh)
frontend/e2e/schemas.spec.ts        Schemas page (list, filter, subject detail, versions, delete) and
                                    the topic Schema tab, incl. their shared query cache
frontend/e2e/schema-messages.spec.ts  decoded Avro records in the messages view and the raw download
                                    of a truncated one (still the wire format, see #87)
frontend/e2e/*.spec.ts              the actual walks
frontend/e2e/csp.spec.ts            fails on any Content-Security-Policy violation (needs the Go-served build)
frontend/e2e/a11y.spec.ts           axe scan of the main routes, dialogs and form error states in light
                                    and dark theme (fixtures/axe.ts); fails on moderate/serious/critical
frontend/e2e/keyboard-and-form-errors.spec.ts  keyboard-only row activation (Tab to the primary cell,
                                    Enter / Space) and aria-invalid + aria-describedby on form errors
frontend/e2e/status-indicators.spec.ts  every status indicator keeps a non-colour cue (WCAG 1.4.1)
frontend/e2e/messages-panel.spec.ts  the topic messages panel: URL search params, browse controls, Load more,
                                    error/partial/empty states, search form and run lifecycle (real and
                                    `page.route`-mocked search responses), coachmark and click-to-filter undo
frontend/e2e/private-cluster-storage.spec.ts  seeds kafkito.private-clusters.v1 and walks the stored
                                    connections; secrets must stay out of the console and the page
frontend/e2e/private-cluster-same-name.spec.ts  the settings form refuses a private cluster named like the
                                    shared one; a stored entry with that name is flagged, and after the
                                    rename each cluster shows its own topics, never the other's cached ones
frontend/e2e/vision-deficiency.spec.ts  opt-in: set KAFKITO_E2E_VISION_DIR to capture the status pages
                                    under deuteranopia / protanopia emulation (Chromium CDP)
Makefile :: e2e, e2e-up, e2e-test, e2e-down
.github/workflows/e2e.yml           CI workflow with browser caching + artifact upload
```

## Authoring conventions

- Each `.spec.ts` is one walk against one route.
- The walk must be **abort-safe**: never commit a destructive op even on
  the local fixture broker — type the confirm phrase, hit Escape, assert
  focus restoration. The point is to walk the gating UI, not to exercise
  mutation code (we have Go integration tests for that).
  The exceptions really run their mutations, each on data it owns:
  - `topic-data.spec.ts`: produce and bulk copy only add records, against
    its own fixture topics (`e2e-copy-source`, `e2e-copy-dest`,
    `e2e-produce-target`) that `seed.sh` recreates on every run.
  - `groups.spec.ts`: creates a consumer group with a unique name, resets
    its offsets and deletes it.
  - `topics.spec.ts`: creates a topic with a unique name, finds it in the
    list without a reload and deletes it through the API afterwards.
  - `acls.spec.ts`: creates an ACL rule for a unique principal, finds it
    in the list and deletes it.
  - `scram-users.spec.ts`: creates a SCRAM user with a unique name,
    rotates its password and deletes the credential.
  - `schemas.spec.ts`: registers its own subject (`e2e-delete-me-value`)
    through the API and deletes it in the UI.
  - The mutation walks require the list refetch caused by the mutation's
    query invalidation and no full page load.
- Cluster name in URLs is `KAFKITO_E2E_CLUSTER` (defaults to `local` —
  the cluster defined in `fixtures/kafkito-e2e.yaml`).
- `clusters.spec.ts`, `status-indicators.spec.ts`,
  `private-cluster-storage.spec.ts` and `private-cluster-same-name.spec.ts`
  test private-cluster
  connections against the fixture broker through the host's private IPv4
  address (`fixtures/host-address.ts`), because the backend refuses
  loopback brokers for private clusters. It picks the first RFC 1918
  interface address; set `KAFKITO_E2E_HOST_IP` to override.
  The fixture broker advertises `localhost:39092`, which the backend refuses
  for private clusters too, so data requests to a private cluster on the
  fixture broker can fail with 502 once the client dials the advertised
  address. Specs that need a private cluster's data answer those requests
  with `page.route` (see `private-cluster-same-name.spec.ts`).

## What is NOT here yet

Out of scope for the current iteration:

- Decoded Protobuf and JSON Schema records in the messages view — the
  fixture topic only carries Avro records
- Cross-cluster switch walks — needs ≥2 fixture clusters
