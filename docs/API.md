# kafkito REST API

Developer reference for the HTTP/JSON surface exposed by `kafkito`. Same
endpoints that back the web UI — stable, documented, scriptable.

## Base URL and auth

- Base URL defaults to wherever you point `kafkito`. When you run it locally
  with `./bin/kafkito --config .local/kafkito.yaml`, that's typically
  `http://localhost:37421`.
- No built-in login. Login and sessions are handled by an upstream auth
  proxy (e.g. SAP Approuter on BTP, oauth2-proxy elsewhere), which forwards
  requests with `Authorization: Bearer <JWT>`. kafkito validates that token
  on every `/api/v1/*` request according to `KAFKITO_AUTH_MODE` (`mock`,
  `xsuaa` in `-tags btp` builds, `off` only in `-tags devauth` builds) and
  answers `401` when it is missing or invalid, before any routing or RBAC:
  `{"error": "unauthorized", "message": "missing bearer token"}` (or
  `"invalid token"`) with `WWW-Authenticate: Bearer realm="kafkito"`.
  `/healthz` and `/readyz` are never authenticated.
- The verified JWT principal is the RBAC identity. The identity header
  (`X-Kafkito-User` by default, configurable via `rbac.identity.header`) is
  only consulted when no principal is present on the request; a
  client-supplied header never overrides a validated token. Note that the
  devauth `off` mode injects a synthetic `dev-user` principal. With no RBAC
  configured, every authenticated caller has full access.
- RBAC permissions name a resource as `type:pattern` (for example
  `group:team-*`). The pattern is `*` (every name), a prefix ending in `*`,
  or an exact name. It is matched against the decoded resource name from
  the path or body, literally: a group, subject or user named `*` is only
  covered by the pattern `*`. The topic, consumer group, topic consumer,
  schema subject and SCRAM user lists only return the names the user may
  view.
- Every cluster route checks a permission; the spec and the sections below
  name it where it is not obvious (for example the raw download needs
  `topic:consume`, the broker list `cluster:view`). A route without one is
  denied with `403` `{"error":"forbidden","code":"rbac_denied"}`; only
  `POST /api/v1/clusters/_test` runs without a resource permission.
- Every response carries an `X-Request-Id` header. It reuses the inbound
  `X-Vcap-Request-Id`, `traceparent` trace-id or `X-Request-Id` when present,
  and matches the `request_id` field in the server logs.
- JSON everywhere. Request bodies: `Content-Type: application/json`. Response
  bodies: list endpoints always return `{ "<resource>": [...] }`, not bare
  arrays, so new fields can be added without breaking clients.
- Every response (API, UI and static files) carries the security headers
  listed in the README under
  [Security headers](https://github.com/FinkeFlo/kafkito/blob/main/README.md#security-headers),
  including a strict `Content-Security-Policy`.

## Private clusters

Clusters a user adds in the UI are stored in their browser only. To address
one from a script, use the path segment `__private__` as `{cluster}` and send
the cluster definition as base64-encoded JSON (`ClusterConfig` in the spec,
at most 8 KiB decoded) in the `X-Kafkito-Cluster` header on every request.
The server keeps nothing between requests.

- A malformed header, a missing header on a `__private__` path, or a broker
  or Schema Registry host the SSRF guard refuses returns `400`. Neither the
  raw header nor the credentials in it appear in a response or a log line.
- RBAC does not apply to private clusters, and their lists are not
  filtered; only the broker's own ACLs apply.
- `POST /api/v1/clusters/_test` probes a cluster definition sent in the body
  (the "Test connection" button). It checks the seed and then every broker
  the cluster advertises; a broker that is blocked for private clusters or
  does not answer is listed in `broker_issues`, and `reachable` is false.
  At most 64 brokers are checked; `brokers_skipped` counts the rest.

```bash
PRIVATE=$(printf '%s' '{"name":"mine","brokers":["broker.example.com:9092"],"auth":{"type":"none"},"tls":{"enabled":false}}' | base64 | tr -d '\n')
curl -s -H "X-Kafkito-Cluster: $PRIVATE" "$BASE/api/v1/clusters/__private__/topics" | jq '.topics[].name'
```

## Contract and live docs

- **The contract** is the OpenAPI 3.1 document
  [`api/openapi.yaml`](https://github.com/FinkeFlo/kafkito/blob/main/api/openapi.yaml) (see
  [ADR-0005](adr/0005-openapi-contract.md)). It lists every endpoint,
  parameter, request/response schema and error shape. The tables below are
  a scripting-oriented summary; when they disagree, the spec wins.
- **Raw OpenAPI 3.1**: `GET /api/v1/openapi.yaml` serves the same document
  from the running binary. Open it in any OpenAPI viewer (e.g. Swagger
  Editor, Redocly, or your IDE's OpenAPI plugin).

## Meta

| Method | Path                   | Purpose                                          |
| ------ | ---------------------- | ------------------------------------------------ |
| GET    | `/healthz`             | Liveness (always 200 while the process is up).   |
| GET    | `/readyz`              | Readiness. 503 if any configured cluster is down.|
| GET    | `/api/v1/info`         | Build name + version.                            |
| GET    | `/api/v1/me`           | Resolved caller identity + effective permissions.|
| GET    | `/api/v1/openapi.yaml` | The OpenAPI document (see above).                |

## Clusters

```bash
# List configured clusters, reachability and capabilities
curl -s $BASE/api/v1/clusters | jq '.clusters[] | {name, reachable, tls, auth_type, schema_registry, caps: .capabilities}'

# Re-probe capabilities (after granting permissions in the broker)
curl -sX POST $BASE/api/v1/clusters/$CLUSTER/capabilities/refresh | jq

# List brokers (id, host, port, rack, controller flag)
curl -s $BASE/api/v1/clusters/$CLUSTER/brokers | jq '.brokers[]'
```

`GET /api/v1/clusters/{cluster}/brokers` requires `view` on `cluster:<cluster>`
with RBAC enabled.

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

The query parameters (`partition`, `limit` capped at 500, `from` =
`end` | `start` | `offset` | `timestamp`, `offset`, `partition_offsets`,
`from_ts_ms`, `to_ts_ms`, `cursor`) and the `MessagesPage` response are
described in the spec. Pass the returned `next_cursor` back as `cursor` to
page. With `from=offset`, the time bounds still apply: the start offset is
lifted to `from_ts_ms` and the page stops before `to_ts_ms`.

Records are returned in per-partition offset order. `value` is populated when
printable; a binary payload is rendered as a `0x…` hex preview with
`value_encoding=binary` and its bytes in `value_b64`. Schema-Registry encoded
records are decoded transparently when an SR is configured for the cluster and
carry a `value_sr` meta block (`schema_id`, `subject`, `version`, `format`).

`value_encoding` is `json` or `xml` when the value's structure was detected,
`text` otherwise. A value over 64 KB (`value_truncated=true`) only has its
first 64 KB in `value`, so `json`/`xml` detection there is a syntactic sniff
(does it start with `{`/`[` or `<`?) rather than full validation — occasionally
wrong for a truncated preview, but the alternative (reporting `text` for every
large JSON/XML record just because truncation broke its structure) is wrong
far more often. Fetch the full value via the raw-download endpoint to get a
definitive answer.

`masked: true` marks a value the cluster's `data_masking` rules changed. The
rules run on the full decoded value (Schema-Registry decoded where
applicable), so a masked field is masked even past the 64 KB preview; a masked
value carries no `value_b64`, and its raw download is refused (see below).
Rules with the `key` or `headers` target (see [Data masking](data-masking.md))
mask keys and header values the same way: `key_masked: true` marks a masked
key, which then carries no `key_b64`, and `masked_headers` lists the sorted
keys of the headers whose values were masked, which then have no
`headers_b64` entry. Header keys are never masked. Rules without `targets`
mask the value only.

`headers` holds header values as text. A header value that is not valid UTF-8
is rendered there as `0x…` hex — display only — and its raw bytes are also
returned in the optional `headers_b64` map (standard base64, only the affected
keys). Use `headers_b64` when you need to reproduce a header byte-for-byte.

```bash
# Most recent 20 records across all partitions
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages?limit=20&from=end" | jq '.messages[] | {p:.partition, off:.offset, ts:.timestamp_ms, enc:.value_encoding}'

# Read from the beginning of partition 0
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages?partition=0&from=start&limit=100" | jq
```

### Count and timeline

`GET /api/v1/clusters/{cluster}/topics/{topic}/messages/count` and
`GET /api/v1/clusters/{cluster}/topics/{topic}/messages/timeline` both resolve
a timestamp window to per-partition offset deltas without consuming any
records — fast, approximate volume metrics rather than exact counts.

- **Count** takes the optional `partition`, `from_ts_ms` and `to_ts_ms` query
  parameters and returns a `MessageCountResponse` (`total_approx_count` plus a
  per-partition breakdown).
- **Timeline** requires `from_ts_ms`, `to_ts_ms` and `slot_ms` (the slot width
  in milliseconds, e.g. `3600000` for hourly) and returns a
  `MessageTimelineResponse` with one approximate count per time slot, capped
  at 400 slots per request.

Both mask nothing (no record data leaves the broker), so they work the same
on topics with `data_masking` rules.

```bash
# Approximate count for the last 24h
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/count?from_ts_ms=$(( ($(date +%s)-86400)*1000 ))&to_ts_ms=$(( $(date +%s)*1000 ))" | jq

# Hourly volume over the last 24h
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/timeline?from_ts_ms=$(( ($(date +%s)-86400)*1000 ))&to_ts_ms=$(( $(date +%s)*1000 ))&slot_ms=3600000" | jq '.slots'
```

### Sample messages

`GET /api/v1/clusters/{cluster}/topics/{topic}/sample`

Returns up to `n` (query parameter, default 5, clamped to 1..25) most recent
decoded messages as a `SampleResponse`. Used by the UI's search path-picker to
build a structural sample of the topic payload; reuses the `topic:consume`
RBAC permission and applies `data_masking` the same way `GET .../messages`
does.

```bash
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/sample?n=10" | jq '.messages[]'
```

### Download raw value

`GET /api/v1/clusters/{cluster}/topics/{topic}/messages/{partition}/{offset}/raw`

Streams the untruncated value of a single record — no base64, no JSON
envelope. This is the endpoint to use whenever the 64 KB preview in
`GET .../messages` is not enough: to inspect a large value in full, to
confirm a truncated preview's real encoding, or to re-produce a record
byte-for-byte.

`partition` and `offset` are exact (no `-1`, no negative offsets).

Query parameters:

- `decoded` (`true`/`false`, default `true`): a value `GET .../messages`
  shows Schema Registry decoded (Avro, JSON Schema) is served as that decoded
  JSON. `decoded=false` serves the stored Confluent wire-format bytes (magic
  byte, 4-byte schema id, payload) instead, e.g. to re-produce the record.
  Only the literal `true` and `false` are accepted.

Response headers:

- `Content-Type`:
  - a decoded value is always `application/json`;
  - a Schema Registry framed value served as stored — with `decoded=false`,
    or because it cannot be decoded (unknown schema id, Schema Registry
    unreachable, Protobuf, which kafkito does not decode yet) — is
    `application/octet-stream`;
  - any other value is sniffed from the bytes: `application/json` for a value
    that starts with `{`/`[` *and* validates as JSON, `application/xml` for
    well-formed XML, `text/plain; charset=utf-8` for any other valid UTF-8,
    `application/octet-stream` otherwise.
- `Content-Disposition: attachment; filename="{topic}-p{partition}-o{offset}.{ext}"`
  with `ext` one of `json`, `xml`, `txt`, `bin`, matching the content type.
- `X-Kafkito-Value-Decoded: avro|json_schema` when the body is the decoded
  JSON; absent when it holds the stored bytes.
- `Content-Length` is the exact byte length of the body.

Status codes:

| Status | When                                                              |
| ------ | ----------------------------------------------------------------- |
| `200`  | Value returned in the body.                                       |
| `400`  | `partition` is not a non-negative int32, `offset` is not a non-negative int64, or `decoded` is not `true`/`false` (`code: invalid_request`). |
| `403`  | RBAC denied `topic:consume` on the topic, or the value is masked (`code: value_masked`). |
| `404`  | Unknown cluster.                                                  |
| `413`  | Value is larger than the 15 MB download cap, before or after decoding. |
| `502`  | Broker error, or no record at that partition/offset (`code: kafka_upstream`). |

The 15 MB cap is fixed (not configurable) so a single oversized record cannot
exhaust process memory. It applies to the stored value and again to the
decoded JSON, which is usually larger than its Avro encoding.

Masked values are not downloadable: when the cluster's `data_masking` rules
change the value of the record (checked on the same decoded rendering
`GET .../messages` masks, so such a record comes back there with
`masked: true`), the endpoint responds `403` with
`{ "error": "value is masked and cannot be downloaded", "code": "value_masked" }`.
Records of topics without a masking rule, and records the rules leave
unchanged, are served as before. The body is the value only, so rules that
mask just the key or headers do not block the download.

Masking is checked the same way for `decoded=false`: the stored bytes carry
the same data.

```bash
# Inspect a large JSON value in full
curl -s "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/0/12345/raw" | jq

# Save a binary value to disk
curl -sOJ "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/0/12345/raw"

# Save the wire-format bytes of a Schema Registry value
curl -sOJ "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/0/12345/raw?decoded=false"

# A value over the cap returns 413
curl -s -o /dev/null -w '%{http_code}\n' \
  "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/0/99999/raw"
```

### Search

`POST /api/v1/clusters/{cluster}/topics/{topic}/messages/search`

Bounded content search with a scan budget. The body is a `SearchRequest`
(see the spec). Common fields: `mode` (`contains` | `jsonpath` | `xpath` |
`js`), `path` (the JSONPath or XPath expression), `op` (`exists` | `eq` |
`contains` | `regex` | …), `value` (the needle, or the JS predicate in `js`
mode), `zones` (e.g. `["value","headers","key"]`), `direction`
(`newest_first` | `oldest_first`), `limit` (matches) and `budget` (records to
scan). The response carries the matches in `messages` and the scan statistics
in `search`; pass `search.next_cursors` back as `cursors` to continue. Bodies
over 1 MiB return `400`.

Quick example — simple contains across message value:

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d '{"value":"customerNumber","zones":["value"],"mode":"contains","direction":"newest_first","limit":20,"budget":5000}' \
  | jq '.search, (.messages[] | {p:.partition, off:.offset})'
```

Advanced: JSONPath example (match messages where isAvailable==true AND language=='English')

```bash
curl -sS -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d "{\"mode\":\"jsonpath\",\"op\":\"exists\",\"path\":\"$..[?(@.isAvailable==true && @.language=='English')]\",\"zones\":[\"value\"],\"limit\":20}" \
  | jq '.search, (.messages[] | {p:.partition, off:.offset})'
```

Advanced: JavaScript predicate example (same logic, runs the predicate per message)

```bash
curl -sS -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages/search" \
  -H 'content-type: application/json' \
  -d "{\"mode\":\"js\",\"value\":\"parsed.isAvailable === true && parsed.language === 'English'\",\"zones\":[\"value\"],\"limit\":20}" \
  | jq '.search, (.messages[] | {p:.partition, off:.offset})'
```

Notes:
- JSONPath filters return nodes; use `op=exists` to treat any match as a hit.
- JS mode receives a parsed JSON object as `parsed` and can express arbitrarily complex predicates. The server enforces a short per-message timeout for JS filters.
- Use `zones` to control where the scanner looks (`value`, `headers`, `key`).
- Matching runs against each record's full, untruncated content, so `contains`/`jsonpath`/`xpath`/`js` all find hits anywhere in large values (there is no size limit on what is *searched*). Only the message previews in the response stay capped at 64 KB per value, same as `GET .../messages` — use the raw-download endpoint to fetch a full value for a hit.
- On topics with a `data_masking` rule, the value is matched in its **masked** form — the same rendering `GET .../messages` returns — so masked content is not searchable in clear text. The same holds for keys and header values on topics with a `key` or `headers` rule (the `contains` key/headers zones and `key`/`headers` in `js`); header keys are not masked and are matched as they are. Parse errors on such topics report `value could not be evaluated (details withheld: data masking applies to this topic)` in `parse_error_offsets[].error` instead of the parser's message, which can quote the value.

### Produce

`POST /api/v1/clusters/{cluster}/topics/{topic}/messages`

```bash
curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/topics/$TOPIC/messages" \
  -H 'content-type: application/json' \
  -d '{"key":"order-1","value":"{\"id\":1}","headers":{"source":"manual"}}' \
  | jq
```

The body is a `ProduceRequest` (see the spec): optional `partition`, `key` and
`value` with their `key_encoding` / `value_encoding`, and `headers` /
`headers_b64`.

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

Header keys starting with `X-Kafkito-` are reserved for kafkito's provenance
headers. kafkito drops every such key from `headers` and `headers_b64`, matched
case-insensitively (`x-kafkito-user` and `X-KAFKITO-USER` are dropped too), then
sets `X-Kafkito-Source: true` and, only when an identity is available,
`X-Kafkito-User: <subject>` (the same subject RBAC resolves). Without an
identity the record carries no `X-Kafkito-User` at all.

The body may be gzip-compressed with `Content-Encoding: gzip`. The body is
capped at 15 MiB of JSON either way (after decompression); a larger body
returns `413` `request body exceeds the 15 MB produce limit`. A record the
client-side 10 MiB batch cap or the broker's `max.message.bytes` refuses
returns `413` with `code: kafka_message_too_large`. Unknown fields are rejected
with `400`.

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

The body is a `CopyRequest` (see the spec for every field). `dest_topic` is
required, and exactly one of `dest_cluster` (a server-configured cluster) /
`dest_cluster_config` (a private cluster, same shape the `X-Kafkito-Cluster`
header carries) must be set. `from_ts_ms` is inclusive, `to_ts_ms` exclusive.

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

Each event is a `data: {json}` line pair carrying a `CopyProgressEvent`:
`copied`, `skipped` (omitted while 0), `done` (final event only) and `error`
(set on the final event if the job aborted).

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
  value, the key or a header value with a redacted rendering, so copying
  would write the redaction.

Copied records carry the same provenance headers the produce endpoint injects —
`X-Kafkito-Source: true` and, when an identity is available,
`X-Kafkito-User: <subject>` of the caller. Every source header whose key starts
with `X-Kafkito-` (case-insensitive) is dropped, so the original producer's
provenance is never carried over.

Status codes returned **before** the stream starts:

| Code | Meaning                                                                                                                                                                  |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 200  | Job started; body is `text/event-stream`.                                                                                                                                |
| 400  | Invalid body (including unknown fields or a body over 32 KiB), missing `dest_topic`, both or neither destination field, destination equal to the source cluster+topic (would never terminate), unknown `dest_cluster`, `dest_topic` does not exist (the destination is never auto-created), or `preserve_partition` with too few destination partitions. |
| 403  | RBAC denied consume on the source or produce on the destination.                                                                                                          |
| 428  | Destination cluster is marked `is_prod` and the `X-Kafkito-Confirm-Prod: true` header is missing.                                                                         |
| 429  | Too many concurrent copy jobs server-wide (4); body carries `code: copy_concurrency_limit` and the response has a `Retry-After: 30` header. Copies hold broker connections for their whole run, so the server sheds load instead of queueing. |

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
(+ `timestamp_ms`), `shift-by` (+ `shift`). `topic` and `strategy` are
required, and an unknown strategy or an unknown body field returns `400`
`invalid_request`. The body is checked before the production confirmation,
so an invalid body on a production cluster returns `400`, not `428`.

Creating a group (`POST /api/v1/clusters/{cluster}/groups`) takes
`group_id`, `topic` and `strategy` (`earliest`, `latest`, `offset`,
`timestamp`; a new group has no offsets to shift, so `shift-by` returns
`400`). Broker
errors return the generic `502` `kafka_upstream` response.

## Schema Registry

```bash
curl -s $BASE/api/v1/clusters/$CLUSTER/schemas/subjects | jq
curl -s $BASE/api/v1/clusters/$CLUSTER/schemas/subjects/$SUBJECT/versions | jq
curl -s $BASE/api/v1/clusters/$CLUSTER/schemas/subjects/$SUBJECT/versions/latest | jq
```

With RBAC enabled, the subject list only returns the subjects the caller
has `schema:<subject>` `view` on.

Registering a schema requires `schema`. `schemaType` is `AVRO` (default),
`JSON` or `PROTOBUF`, spelled exactly; every reference needs `name`,
`subject` and `version`. Unknown body fields return `400`, and bodies over
2 MiB return `400` `invalid body: http: request body too large`.

`DELETE .../schemas/subjects/{subject}?permanent=true` hard-deletes the
subject. `permanent` accepts only `true` and `false`; any other value,
including an empty one, returns `400` instead of a soft delete.

## ACLs

```bash
curl -s $BASE/api/v1/clusters/$CLUSTER/acls | jq

curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/acls" \
  -H 'content-type: application/json' \
  -d '{"principal":"User:alice","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}' \
  | jq
```

Create (`POST`) and delete (`DELETE` with the filter as JSON body) take all
seven fields, `host` included; a missing field returns `400`
`invalid_request` (`host` has no default). Enum-like values are
case-insensitive. Bodies are capped at 16 KiB.

## SCRAM users

```bash
curl -s $BASE/api/v1/clusters/$CLUSTER/users | jq

curl -s -X POST "$BASE/api/v1/clusters/$CLUSTER/users" \
  -H 'content-type: application/json' \
  -d '{"user":"alice","mechanism":"SCRAM-SHA-512","password":"s3cret","iterations":8192}' \
  | jq
```

`mechanism` must be exactly `SCRAM-SHA-256` or `SCRAM-SHA-512`, in the body
and in the `mechanism` query of `DELETE .../users/{user}`. Aliases such as
`SHA-256` or lower case return `400`. Omit the query parameter to delete
both mechanisms; an empty `?mechanism=` returns `400`. The password never
appears in a response or a log line, not even in validation errors.

With RBAC enabled, the list only returns the users the caller has
`user:<name>` `view` on, and creating or updating a credential needs `edit`
on the `user` named in the body (`403` otherwise; a body without `user`
returns `400`). The name is checked as sent, surrounding whitespace
included.

## Errors

All error responses share the `Error` schema from the spec:

```json
{ "error": "human-readable message", "code": "optional_machine_code" }
```

`error` is always present. `code` is set where a machine-readable code
exists; the spec's `Error` schema lists them (`kafka_upstream`,
`private_cluster_address_blocked`, `invalid_request`, `value_masked`, `production_confirmation_required`,
`copy_concurrency_limit`, …). RBAC denials add `resource` and `action`, and
401s from the auth middleware add `message`. Upstream Kafka/Schema Registry
details are only logged server-side; the response carries
`"error": "upstream kafka error"`. A private cluster whose broker or Schema
Registry address the outbound guard refuses gets `502` with
`"code": "private_cluster_address_blocked"` and a static message; Test
connection names the broker. Error bodies never contain credentials or
the raw `X-Kafkito-Cluster` header.

Every request is validated against `api/openapi.yaml` (see
[ADR-0005](adr/0005-openapi-contract.md)) before the handler runs. A mismatch returns `400` with
`"code": "invalid_request"`, and `error` names the parameter or body field
and the violated rule, but never the submitted value:

```json
{ "error": "request body \"/auth/type\": must be one of the allowed values", "code": "invalid_request" }
```

A body that is not valid JSON returns `400` `request body: malformed`, and a
field the schema does not allow returns `400`
`request body: has properties that are not allowed`. A body over an
endpoint's size limit returns `400` `invalid body: http: request body too
large` (`invalid json: …` for ACLs and SCRAM users, `invalid json body: …` for
search); only the produce endpoint answers `413`.

JSON request bodies must be sent with
`Content-Type: application/json` (a `charset` parameter is fine); other
content types return `400` `request body: unsupported Content-Type`.

`X-Kafkito-Confirm-Prod` must be exactly `true` when sent;
any other value returns `400` before the production check runs. Omit the
header to get the `428` `production_confirmation_required` response.

Status codes used by the server:

| Code | Meaning                                                         |
| ---- | --------------------------------------------------------------- |
| 400  | Request body/query parameter/header is invalid.                 |
| 401  | Bearer token missing or invalid (see [Base URL and auth](#base-url-and-auth)). |
| 403  | RBAC denied the action, the broker denied the credential (`kafka_not_authorized`), or the value is masked (`value_masked`). |
| 404  | Cluster/topic/group/subject not found.                          |
| 409  | Conflict (topic already exists, group not empty, etc.).         |
| 413  | Produce body over 15 MiB, record too large for the broker, or raw value over 15 MB. |
| 428  | Production cluster needs `X-Kafkito-Confirm-Prod: true`.        |
| 429  | Too many concurrent long-running jobs (e.g. topic copies).      |
| 502  | Kafka broker or Schema Registry returned an error.              |
| 504  | Request to Kafka/SR timed out.                                  |

## Shell setup used in examples

```bash
export BASE=http://localhost:37421
export CLUSTER=local
export TOPIC=orders
export GROUP=orders-consumer
```
