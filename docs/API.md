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

`headers` holds header values as text. A header value that is not valid UTF-8
is rendered there as `0x…` hex — display only — and its raw bytes are also
returned in the optional `headers_b64` map (standard base64, only the affected
keys). Use `headers_b64` when you need to reproduce a header byte-for-byte.

```bash
# Most recent 20 records across all partitions
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages?limit=20&from=latest" | jq '.messages[] | {p:.partition, off:.offset, ts:.timestamp_ms, enc:.value_encoding}'

# Read from the beginning of partition 0
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages?partition=0&from=oldest&limit=100" | jq
```

### Search

`POST /api/v1/clusters/{cluster}/topics/{topic}/messages/search`

Bounded content search with a scan budget. Request body is a JSON object describing the scan (mode, path/value, zones, limit, budget). Common fields: `mode` (contains|jsonpath|xpath|js), `path` (for path modes), `op` (exists|eq|contains|regex|...), `value`, `zones` (array, e.g. ["value","headers","key"]).

Quick example — simple contains across message value:

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d '{"query":"customerNumber","zones":["value"],"mode":"contains","direction":"backward","limit":20,"max_scan":5000}' \
  | jq '.stats, (.messages[] | {p:.partition, off:.offset})'
```

Advanced: JSONPath example (match messages where isAvailable==true AND language=='English')

```bash
curl -sS -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d "{\"mode\":\"jsonpath\",\"op\":\"exists\",\"path\":\"$..[?(@.isAvailable==true && @.language=='English')]\",\"zones\":[\"value\"],\"limit\":20}" \
  | jq '.stats, (.messages[] | {p:.partition, off:.offset})'
```

Advanced: JavaScript predicate example (same logic, runs the predicate per message)

```bash
curl -sS -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d "{\"mode\":\"js\",\"value\":\"parsed.isAvailable === true && parsed.language === 'English'\",\"zones\":[\"value\"],\"limit\":20}" \
  | jq '.stats, (.messages[] | {p:.partition, off:.offset})'
```

Notes:
- JSONPath filters return nodes; use `op=exists` to treat any match as a hit.
- JS mode receives a parsed JSON object as `parsed` and can express arbitrarily complex predicates. The server enforces a short per-message timeout for JS filters.
- Use `zones` to control where the scanner looks (`value`, `headers`, `key`).

### Produce

`POST /api/v1/clusters/{cluster}/topics/{topic}/messages`

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages" \
  -H 'content-type: application/json' \
  -d '{"key":"order-1","value":"{\"id\":1}","headers":{"source":"manual"}}' \
  | jq
```

| Field                             | Notes                                                    |
| --------------------------------- | -------------------------------------------------------- |
| `partition`                       | Optional. Omit to let the partitioner choose.             |
| `key` / `value`                   | Payloads, interpreted per the matching `*_encoding`.      |
| `key_encoding` / `value_encoding` | `text` (default), `base64` or `empty`.                    |
| `headers`                         | Header values as UTF-8 text.                              |
| `headers_b64`                     | Header values as standard base64 raw bytes.               |

Encodings:

- `text` (default) — the string is sent as UTF-8 bytes. An **empty** `text`
  value produces a **nil** payload, i.e. a tombstone. On a compacted topic that
  deletes the key, so the distinction matters.
- `base64` — the string is base64-decoded (standard, URL-safe and raw standard
  are all accepted), for arbitrary binary payloads. An empty string produces a
  nil payload here too.
- `empty` — the string is ignored and a non-nil **zero-length** payload is
  produced. This is the only way to express a zero-length value, since `text`
  with an empty string means "tombstone".

Header values that are not valid UTF-8 cannot round-trip through `headers`; pass
them as base64 raw bytes in `headers_b64` instead. A key present in both maps
wins in `headers_b64` and is emitted exactly once; an undecodable value fails the
request with 400.

kafkito always injects `X-Kafkito-Source: true` and, when an identity is
available, `X-Kafkito-User: <subject>`, overwriting those keys if you supplied
them.

```bash
# Zero-length value (not a tombstone) plus a binary header
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages" \
  -H 'content-type: application/json' \
  -d '{"key":"order-1","value_encoding":"empty","headers_b64":{"trace-id":"AAECAw=="}}' \
  | jq

# Tombstone: empty text value produces a nil payload
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages" \
  -H 'content-type: application/json' \
  -d '{"key":"order-1","value":""}' \
  | jq
```

### Copy messages to another topic

`POST /api/v1/clusters/{cluster}/topics/{topic}/copy`

Server-side bulk copy. The `{cluster}`/`{topic}` in the URL are the **source**;
the destination is named in the body. The destination topic must already exist —
kafkito does not create it.

The response is **not** JSON: it is a `text/event-stream` of progress events,
because a copy can run far longer than a normal request.

| Field                 | Type          | Notes                                                                                          |
| --------------------- | ------------- | ---------------------------------------------------------------------------------------------- |
| `dest_cluster`        | string        | Name of a server-configured destination cluster. Mutually exclusive with `dest_cluster_config`. |
| `dest_cluster_config` | object        | Ad-hoc ("private") destination cluster, same shape the `X-Kafkito-Cluster` header carries.      |
| `dest_topic`          | string        | **Required.** Destination topic.                                                                |
| `partition`           | int32         | Single source partition. Absent = all partitions.                                               |
| `from_ts_ms`          | int64         | Inclusive lower bound on source record timestamps.                                              |
| `to_ts_ms`            | int64         | **Exclusive** upper bound. See below.                                                           |
| `limit`               | int64         | Max records to copy. Absent = no limit.                                                         |
| `preserve_partition`  | bool          | Produce each record to the partition number it came from.                                       |

Exactly one of `dest_cluster` / `dest_cluster_config` must be set.

When `to_ts_ms` is omitted the server substitutes the job's start time, so a
copy of a live topic terminates instead of tailing it forever: records produced
after the copy started are not included.

`preserve_partition` requires the destination topic to have at least as many
partitions as the highest source partition, otherwise the request is rejected.

```bash
# Copy the last hour of a topic into another topic on the same cluster
curl -sN -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/copy" \
  -H 'content-type: application/json' \
  -d '{"dest_cluster":"'$CLUSTER'","dest_topic":"'$TOPIC'_replay","from_ts_ms":'$(( ($(date +%s) - 3600) * 1000 ))'}'
```

```
data: {"copied":0}

data: {"copied":500,"skipped":3}

data: {"copied":812,"skipped":5,"done":true}
```

Each event is a `data: {json}` line pair with the fields:

| Field     | Notes                                                                     |
| --------- | ------------------------------------------------------------------------- |
| `copied`  | Records produced to the destination so far.                               |
| `skipped` | Records deliberately left out (see below). Omitted while 0.               |
| `done`    | `true` on the final event only; omitted otherwise.                        |
| `error`   | Set on the final event if the job aborted. Omitted when empty.            |

Progress events arrive periodically — one right after the stream opens and at
least one per fetched page. Because the SSE headers are sent before the copy
starts, a failure *during* the copy surfaces as a `done` event carrying `error`
**with HTTP status 200**: inspect the events, not just the status code.

Watch the stream with `jq`:

```bash
curl -sN -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/copy" \
  -H 'content-type: application/json' \
  -d '{"dest_cluster":"other-cluster","dest_topic":"orders","limit":1000}' \
  | sed -u 's/^data: //' | jq -c --unbuffered
```

`skipped` counts source records that cannot be reproduced byte-for-byte and are
therefore left out rather than copied approximately:

- **Schema-Registry-decoded payloads** (`avro`, `json_schema`, `protobuf`):
  only the decoded JSON rendering is available, the original wire-format bytes
  are gone.
- **Masked records**: the source cluster's `data_masking` rules replaced the
  value with a redacted rendering, so copying would write the redaction.

Copied records carry the same provenance headers the produce endpoint injects —
`X-Kafkito-Source: true` and `X-Kafkito-User: <subject>` — overwriting those
keys if the source record already had them.

Status codes returned **before** the stream starts:

| Code | Meaning                                                                                                                                                                  |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 200  | Job started; body is `text/event-stream`.                                                                                                                                |
| 400  | Invalid body, missing `dest_topic`, both or neither destination field, destination equal to the source cluster+topic (would never terminate), unknown `dest_cluster`, `dest_topic` does not exist (the destination is never auto-created), or `preserve_partition` with too few destination partitions. |
| 403  | RBAC denied consume on the source or produce on the destination.                                                                                                          |
| 428  | Destination cluster is marked `is_prod` and the `X-Kafkito-Confirm-Prod: true` header is missing.                                                                         |
| 429  | Too many concurrent copy jobs server-wide; body carries `code: copy_concurrency_limit` and the response has a `Retry-After` header. Copies hold broker connections for their whole run, so the server sheds load instead of queueing. |

**Authorization.** The source is checked as `topic:consume` by the RBAC
middleware (from the URL); the destination is checked as `topic:produce` by the
handler, against the cluster/topic named in the body.

An ad-hoc `dest_cluster_config` destination **bypasses RBAC entirely** — the
caller supplies their own broker credentials and only the destination broker's
own ACLs apply. This is a deliberate, pre-existing property of private clusters,
but it means a user holding nothing but `topic:consume` can stream a readable
topic to a broker of their choosing. Operators who care about egress should
disable private clusters rather than rely on the copy endpoint's RBAC checks.

**Not transactional, not resumable.** An error leaves the records copied so far
in the destination topic, and re-running the copy duplicates them.

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
| 428  | Production cluster needs `X-Kafkito-Confirm-Prod: true`.        |
| 429  | Too many concurrent long-running jobs (e.g. topic copies).      |
| 502  | Kafka broker returned an error.                                 |
| 504  | Request to Kafka/SR timed out.                                  |

## Shell setup used in examples

```bash
export BASE=http://localhost:37421
export CLUSTER=spinedev-preview
export TOPIC=FRA_acme_eXtend_SalesPrices_DEV
export GROUP=FRA_acme_IF_H001_Acme_Post_Prices_Example
```
