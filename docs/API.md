# kafkito REST API

Developer reference for the HTTP/JSON surface exposed by `kafkito`. Same
endpoints that back the web UI — stable, documented, scriptable.

## Base URL and auth

- Base URL defaults to wherever you point `kafkito`. When you run it locally
  with `./bin/kafkito --config .local/kafkito.yaml`, that's typically
  `http://localhost:37421`.
- No built-in login. If RBAC is configured, identity is read from the
  `X-User` header (forwarded by your reverse proxy). With no RBAC configured,
  the API is open.
- JSON everywhere. Request bodies: `Content-Type: application/json`. Response
  bodies: list endpoints always return `{ "<resource>": [...] }`, not bare
  arrays, so new fields can be added without breaking clients.

## Live docs

- **Swagger UI**: `GET /api/v1/docs` — interactive, try-it-out enabled.
- **Raw OpenAPI 3.1**: `GET /api/v1/openapi.yaml`.

## Meta

| Method | Path                | Purpose                                          |
| ------ | ------------------- | ------------------------------------------------ |
| GET    | `/healthz`          | Liveness (always 200 while the process is up).   |
| GET    | `/readyz`           | Readiness. 503 if any configured cluster is down.|
| GET    | `/api/v1/info`      | Build name + version.                            |
| GET    | `/api/v1/me`        | Resolved caller identity + effective permissions.|

## Clusters

```bash
# List configured clusters, reachability and capabilities
curl -s $BASE/api/v1/clusters | jq '.clusters[] | {name, reachable, tls, auth_type, schema_registry, caps: .capabilities}'

# Re-probe capabilities (after granting permissions in the broker)
curl -sX POST $BASE/api/v1/clusters/$CLUSTER/capabilities/refresh | jq
```

## Topics

```bash
# List
curl -s $BASE/api/v1/clusters/$CLUSTER/topics | jq '.topics[] | {name, partitions, replication_factor, is_internal}'

# Describe one topic (partitions, leaders, ISR, low/high watermarks, configs)
curl -s $BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC | jq

# Which consumer groups are on this topic?
curl -s $BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/consumers | jq
```

### Consume messages

`GET /api/v1/clusters/{cluster}/topics/{topic}/messages`

| Query       | Default | Notes                                                |
| ----------- | ------- | ---------------------------------------------------- |
| `partition` | `-1`    | `-1` = all partitions.                               |
| `limit`     | `50`    | Server caps at 500.                                  |
| `from`      | `latest`| `latest` / `oldest` / `offset`.                      |
| `offset`    | —       | Required when `from=offset`.                         |

Response: `{ "messages": [Message, ...] }`. Records are returned in per-partition
offset order. `value` is populated when printable; binary payloads come through
as `value_b64` with `value_encoding=binary`. Schema-Registry encoded records
are decoded transparently when an SR is configured for the cluster and carry a
`value_sr` meta block (`schema_id`, `subject`, `version`, `format`).

```bash
# Most recent 20 records across all partitions
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages?limit=20&from=latest" | jq '.messages[] | {p:.partition, off:.offset, ts:.timestamp_ms, enc:.value_encoding}'

# Read from the beginning of partition 0
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages?partition=0&from=oldest&limit=100" | jq
```

### Search

`POST /api/v1/clusters/{cluster}/topics/{topic}/messages/search`

Bounded content search with a scan budget.

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d '{"query":"customerNumber","zones":["value"],"mode":"contains","direction":"backward","limit":20,"max_scan":5000}' \
  | jq '.stats, (.messages[] | {p:.partition, off:.offset})'
```

### Produce

`POST /api/v1/clusters/{cluster}/topics/{topic}/messages`

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages" \
  -H 'content-type: application/json' \
  -d '{"key":"order-1","value":"{\"id\":1}","headers":{"source":"manual"}}' \
  | jq
```

## Consumer groups

The most useful endpoints when debugging rebalancing:

```bash
# Overview
curl -s $BASE/api/v1/clusters/$CLUSTER/groups | jq '.groups[] | {group_id, state, members, topics, lag}'

# Full detail: members + offsets + coordinator + protocol
curl -s $BASE/api/v1/clusters/$CLUSTER/groups/$GROUP | jq

# Per-member info (who is joined, from which host, with which assignments)
curl -s $BASE/api/v1/clusters/$CLUSTER/groups/$GROUP \
  | jq '.members[] | {client_id, client_host, member_id, instance_id, assignments}'

# Per-partition state (owner is client_id@host; empty during rebalance)
curl -s $BASE/api/v1/clusters/$CLUSTER/groups/$GROUP \
  | jq '.offsets[] | {topic, partition, offset, log_end, lag, owner:.assigned_to}'

# Live polling: re-fetches every second. Great during rebalance storms.
watch -n1 "curl -s $BASE/api/v1/clusters/$CLUSTER/groups/$GROUP \
  | jq '{state, members:(.members|length), offsets:[.offsets[]|{p:.partition,off:.offset,lag,owner:.assigned_to}]}'"
```

Signals to look for when a client keeps rebalancing:

- `state` rapidly toggling between `Stable` and `PreparingRebalance` →
  session/heartbeat timing issue or consumers dying and rejoining.
- Every tick a **new** `member_id` suffix (UUID portion) with the same
  `client_id` → static membership is not configured. Set `group.instance.id`
  in your consumer to keep a stable identity across restarts.
- `protocol` is `range`/`roundrobin` instead of `cooperative-sticky` → plain
  rebalancing moves all partitions every time; cooperative-sticky only moves
  the deltas and is usually what you want.

### Reset offsets

`POST /api/v1/clusters/{cluster}/groups/{group}/reset-offsets`

Group must be empty (no active members). Always try with `"dry_run": true`
first and inspect `results[]`.

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/groups/$GROUP/reset-offsets" \
  -H 'content-type: application/json' \
  -d '{"topic":"'$TOPIC'","strategy":"earliest","dry_run":true}' \
  | jq
```

Strategies: `earliest`, `latest`, `offset` (+ `offset`), `timestamp`
(+ `timestamp_ms`), `shift-by` (+ `shift`).

## Schema Registry

```bash
curl -s $BASE/api/v1/clusters/$CLUSTER/schemas/subjects | jq
curl -s $BASE/api/v1/clusters/$CLUSTER/schemas/subjects/$SUBJECT/versions | jq
curl -s $BASE/api/v1/clusters/$CLUSTER/schemas/subjects/$SUBJECT/versions/latest | jq
```

## ACLs

```bash
curl -s $BASE/api/v1/clusters/$CLUSTER/acls | jq

curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/acls" \
  -H 'content-type: application/json' \
  -d '{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}' \
  | jq
```

## SCRAM users

```bash
curl -s $BASE/api/v1/clusters/$CLUSTER/users | jq

curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/users" \
  -H 'content-type: application/json' \
  -d '{"user":"alice","mechanism":"SCRAM-SHA-512","password":"s3cret","iterations":8192}' \
  | jq
```

## Errors

All error responses look like:

```json
{ "error": "short machine-friendly description", "detail": "optional longer text" }
```

Status codes used by the server:

| Code | Meaning                                                         |
| ---- | --------------------------------------------------------------- |
| 400  | Request body/query parameter is invalid.                        |
| 401  | Authentication required (proxy did not set `X-User`).           |
| 403  | RBAC denied the requested action on the resource.               |
| 404  | Cluster/topic/group/subject not found.                          |
| 409  | Conflict (topic already exists, group not empty, etc.).         |
| 502  | Kafka broker returned an error.                                 |
| 504  | Request to Kafka/SR timed out.                                  |

## Shell setup used in examples

```bash
export BASE=http://localhost:37421
export CLUSTER=spinedev-preview
export TOPIC=FRA_aspire_eXtend_SalesPrices_DEV
export GROUP=FRA_aspire_IF_H001_LX10525_AsPIRe_Pricefx_Post_Prices_Portello
```
