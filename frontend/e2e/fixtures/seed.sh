#!/usr/bin/env bash
# Seed the local Kafka broker (kafkito-kafka container, started by
# `docker compose up -d kafka`) with the deterministic fixture state the
# Playwright walks need:
#
#   topic e2e-walk-target      4 partitions, 12 messages
#   topic e2e-walk-large       1 partition, 50 messages (Delete-Records walk)
#   topic e2e-large-message    1 partition, 1 JSON message ~100 KB (large-messages walk)
#   topic e2e-large-message-xml 1 partition, 1 XML message ~100 KB (large-messages walk)
#   topic e2e-root-array       1 partition, 1 JSON message whose value is an array
#   topic e2e-copy-source      1 partition, 1500 messages (bulk-copy walk: three copy pages)
#   topic e2e-copy-dest        1 partition, empty (bulk-copy walk destination)
#   topic e2e-produce-target   1 partition, empty (produce / search walk)
#   topic e2e-masked           1 partition, 2 JSON messages whose customer.email is
#                              masked by kafkito-e2e.yaml (masking walk); the
#                              second is ~100 KB
#   topic e2e-masked-kh        1 partition, 1 record whose key and authorization
#                              header kafkito-e2e.yaml masks (masking walk)
#   topic e2e-masked-kh-dest   1 partition, empty (masking walk copy destination)
#   consumer group e2e-idle-group  in Empty state (consumed once, then exited)
#
# and the Schema Registry (kafkito-schema-registry container, host port
# 38081) with the subjects the schema walks need:
#
#   subject e2e-avro-orders-value     AVRO, v1 + v2 (v2 adds currency and note),
#                                     compatibility BACKWARD
#   subject e2e-json-customers-value  JSON Schema, v1, compatibility FULL
#   subject e2e-proto-events-value    PROTOBUF, v1, compatibility NONE
#   topic e2e-avro-orders             1 partition, 3 Avro records in the
#                                     Confluent wire format (one v1, two v2;
#                                     the last decodes to ~100 KB of JSON)
#
# Idempotent: safe to re-run; topics are recreated, subjects are hard-deleted
# and registered again, the consumer is run briefly to bring the group back
# to Empty.

set -euo pipefail

BROKER_INTERNAL="kafka:9092"
CONTAINER="${KAFKITO_E2E_KAFKA_CONTAINER:-kafkito-kafka}"
SR_URL="${KAFKITO_E2E_SR_URL:-http://localhost:38081}"
SR_CONTENT_TYPE="Content-Type: application/vnd.schemaregistry.v1+json"

# require_tools fails early, with a hint, when a tool the seeding needs is
# missing: docker for the broker, curl and jq for the Schema Registry REST
# calls, go for the Avro encoder.
require_tools() {
  local missing=()
  local tool
  for tool in docker curl jq go; do
    command -v "${tool}" >/dev/null 2>&1 || missing+=("${tool}")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    echo "seed: missing required tool(s): ${missing[*]}" >&2
    echo "seed: install them and retry (jq and curl: 'brew install jq curl' on macOS," >&2
    echo "seed: 'sudo apt-get install -y jq curl' on Debian/Ubuntu); see the prerequisites in frontend/e2e/README.md" >&2
    exit 1
  fi
}

run_in_kafka() {
  docker exec "${CONTAINER}" "$@"
}

run_in_kafka_stdin() {
  docker exec -i "${CONTAINER}" "$@"
}

wait_for_broker() {
  local tries=30
  while ! run_in_kafka /opt/kafka/bin/kafka-broker-api-versions.sh \
    --bootstrap-server "${BROKER_INTERNAL}" >/dev/null 2>&1; do
    tries=$((tries - 1))
    if [ "${tries}" -le 0 ]; then
      echo "seed: broker did not become reachable in time" >&2
      exit 1
    fi
    sleep 1
  done
}

recreate_topic() {
  local name="$1"
  local partitions="$2"
  # Optional extra --config KEY=VALUE (e.g. a topic-level retention.ms
  # override for fixtures that backdate messages beyond the broker's
  # default KAFKA_LOG_RETENTION_HOURS).
  local extra_config="${3:-}"
  run_in_kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${BROKER_INTERNAL}" --delete --topic "${name}" \
    >/dev/null 2>&1 || true
  if [ -n "${extra_config}" ]; then
    run_in_kafka /opt/kafka/bin/kafka-topics.sh \
      --bootstrap-server "${BROKER_INTERNAL}" --create --if-not-exists \
      --topic "${name}" --partitions "${partitions}" --replication-factor 1 \
      --config "${extra_config}" \
      >/dev/null
  else
    run_in_kafka /opt/kafka/bin/kafka-topics.sh \
      --bootstrap-server "${BROKER_INTERNAL}" --create --if-not-exists \
      --topic "${name}" --partitions "${partitions}" --replication-factor 1 \
      >/dev/null
  fi
}

produce_lines() {
  local topic="$1"
  local n="$2"
  local payload=""
  for i in $(seq 1 "${n}"); do
    payload+="seed-message-${i}"$'\n'
  done
  printf '%s' "${payload}" | run_in_kafka_stdin /opt/kafka/bin/kafka-console-producer.sh \
    --bootstrap-server "${BROKER_INTERNAL}" --topic "${topic}" >/dev/null 2>&1
}

produce_spread_lines() {
  local topic="$1"
  local tmp_go
  local broker_host="${KAFKITO_E2E_BROKER_HOST:-localhost:39092}"
  local repo_root
  repo_root=$(git rev-parse --show-toplevel)
  tmp_go=$(mktemp "${repo_root}/kafkito-seed-produce.XXXXXX.go")
  trap 'rm -f "${tmp_go}"' RETURN
  cat > "${tmp_go}" <<'EOF'
package main

import (
  "bufio"
  "context"
  "fmt"
  "os"
  "strconv"
  "strings"
  "time"

  "github.com/twmb/franz-go/pkg/kgo"
)

func main() {
  if len(os.Args) < 3 {
    fmt.Fprintln(os.Stderr, "usage: seed-produce <broker> <topic>")
    os.Exit(2)
  }
  broker := os.Args[1]
  topic := os.Args[2]

  cl, err := kgo.NewClient(kgo.SeedBrokers(broker))
  if err != nil {
    fmt.Fprintln(os.Stderr, "seed: create client:", err)
    os.Exit(1)
  }
  defer cl.Close()

  sc := bufio.NewScanner(os.Stdin)
  // Default bufio.Scanner max token size is 64 KB — too small for the
  // large-message fixture's ~100 KB line. Raise it to 8 MB (matching the
  // largest fixture kafkito itself is expected to handle).
  sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
  for sc.Scan() {
    line := strings.TrimSpace(sc.Text())
    if line == "" {
      continue
    }
    parts := strings.SplitN(line, "\t", 2)
    if len(parts) != 2 {
      fmt.Fprintln(os.Stderr, "seed: invalid record:", line)
      os.Exit(1)
    }
    ts, err := strconv.ParseInt(parts[0], 10, 64)
    if err != nil {
      fmt.Fprintln(os.Stderr, "seed: invalid timestamp:", parts[0], err)
      os.Exit(1)
    }
    rec := &kgo.Record{
      Topic:     topic,
      Value:     []byte(parts[1]),
      Timestamp: time.UnixMilli(ts),
    }
    if err := cl.ProduceSync(context.Background(), rec).FirstErr(); err != nil {
      fmt.Fprintln(os.Stderr, "seed: produce failed:", err)
      os.Exit(1)
    }
  }
  if err := sc.Err(); err != nil {
    fmt.Fprintln(os.Stderr, "seed: scan failed:", err)
    os.Exit(1)
  }
}
EOF
  cd "${repo_root}" && go run "${tmp_go}" "${broker_host}" "${topic}"
}

# make e2e-up starts the registry in the background (it is not needed until
# here), so this wait also covers its image pull and JVM start.
wait_for_schema_registry() {
  local tries=120
  while ! curl -fsS "${SR_URL}/subjects" >/dev/null 2>&1; do
    tries=$((tries - 1))
    if [ "${tries}" -le 0 ]; then
      echo "seed: schema registry did not become reachable at ${SR_URL} in 120s" >&2
      if [ -n "${KAFKITO_E2E_SR_UP_LOG:-}" ] && [ -f "${KAFKITO_E2E_SR_UP_LOG}" ]; then
        echo "seed: output of 'docker compose up -d schema-registry':" >&2
        cat "${KAFKITO_E2E_SR_UP_LOG}" >&2
      fi
      docker logs --tail 40 kafkito-schema-registry >&2 2>&1 || true
      exit 1
    fi
    sleep 1
  done
}

# reset_subject hard-deletes a subject so the next registration starts at
# version 1 again. Confluent SR only hard-deletes a soft-deleted subject, so
# both calls are needed; either one 404s when the subject is already gone.
reset_subject() {
  local subject="$1"
  curl -sS -o /dev/null -X DELETE "${SR_URL}/subjects/${subject}" || true
  curl -sS -o /dev/null -X DELETE "${SR_URL}/subjects/${subject}?permanent=true" || true
}

set_compatibility() {
  local subject="$1"
  local level="$2"
  jq -n --arg level "${level}" '{compatibility: $level}' |
    curl -fsS -X PUT -H "${SR_CONTENT_TYPE}" --data @- "${SR_URL}/config/${subject}" >/dev/null
}

# register_schema registers the next version of a subject and prints the
# global schema id the registry assigned to it.
register_schema() {
  local subject="$1"
  local schema_type="$2"
  local schema="$3"
  jq -n --arg type "${schema_type}" --arg schema "${schema}" '{schemaType: $type, schema: $schema}' |
    curl -fsS -X POST -H "${SR_CONTENT_TYPE}" --data @- "${SR_URL}/subjects/${subject}/versions" |
    jq -er '.id'
}

# produce_avro_records reads "<timestamp-ms>\t<schema-id>\t<json>" lines,
# encodes each JSON record with the Avro schema the registry holds for that
# id, and produces it in the Confluent wire format: magic byte 0x00, the
# schema id as a big-endian uint32, then the Avro binary body.
produce_avro_records() {
  local topic="$1"
  local tmp_go
  local broker_host="${KAFKITO_E2E_BROKER_HOST:-localhost:39092}"
  local repo_root
  repo_root=$(git rev-parse --show-toplevel)
  tmp_go=$(mktemp "${repo_root}/kafkito-seed-avro.XXXXXX.go")
  trap 'rm -f "${tmp_go}"' RETURN
  cat > "${tmp_go}" <<'EOF'
package main

import (
  "bufio"
  "context"
  "encoding/binary"
  "encoding/json"
  "fmt"
  "net/http"
  "os"
  "strconv"
  "strings"
  "time"

  "github.com/hamba/avro/v2"
  "github.com/twmb/franz-go/pkg/kgo"
)

func fail(msg string, err error) {
  fmt.Fprintln(os.Stderr, "seed:", msg, err)
  os.Exit(1)
}

// schemaByID fetches a schema through the registry's REST API.
func schemaByID(srURL string, id int) avro.Schema {
  res, err := http.Get(fmt.Sprintf("%s/schemas/ids/%d", srURL, id))
  if err != nil {
    fail("fetch schema:", err)
  }
  defer res.Body.Close()
  if res.StatusCode != http.StatusOK {
    fail("fetch schema:", fmt.Errorf("id %d: HTTP %d", id, res.StatusCode))
  }
  var body struct {
    Schema string `json:"schema"`
  }
  if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
    fail("decode schema response:", err)
  }
  s, err := avro.Parse(body.Schema)
  if err != nil {
    fail("parse schema:", err)
  }
  return s
}

// toAvroNative turns a flat JSON object into the Go types hamba/avro
// expects: JSON numbers become int32/int64 for int/long fields.
func toAvroNative(s avro.Schema, raw string) map[string]any {
  dec := json.NewDecoder(strings.NewReader(raw))
  dec.UseNumber()
  var in map[string]any
  if err := dec.Decode(&in); err != nil {
    fail("decode record:", err)
  }
  rec, ok := s.(*avro.RecordSchema)
  if !ok {
    fail("schema:", fmt.Errorf("want a record schema, got %s", s.Type()))
  }
  out := make(map[string]any, len(in))
  for _, f := range rec.Fields() {
    v, ok := in[f.Name()]
    if !ok {
      continue
    }
    if n, isNum := v.(json.Number); isNum {
      i, err := n.Int64()
      if err != nil {
        fail("field "+f.Name()+":", err)
      }
      if f.Type().Type() == avro.Int {
        v = int32(i)
      } else {
        v = i
      }
    }
    out[f.Name()] = v
  }
  return out
}

func main() {
  if len(os.Args) < 4 {
    fmt.Fprintln(os.Stderr, "usage: seed-avro <broker> <schema-registry-url> <topic>")
    os.Exit(2)
  }
  broker, srURL, topic := os.Args[1], os.Args[2], os.Args[3]

  cl, err := kgo.NewClient(kgo.SeedBrokers(broker))
  if err != nil {
    fail("create client:", err)
  }
  defer cl.Close()

  schemas := map[int]avro.Schema{}
  sc := bufio.NewScanner(os.Stdin)
  sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
  for sc.Scan() {
    line := strings.TrimSpace(sc.Text())
    if line == "" {
      continue
    }
    parts := strings.SplitN(line, "\t", 3)
    if len(parts) != 3 {
      fail("invalid record:", fmt.Errorf("%q", line))
    }
    ts, err := strconv.ParseInt(parts[0], 10, 64)
    if err != nil {
      fail("invalid timestamp:", err)
    }
    id, err := strconv.Atoi(parts[1])
    if err != nil {
      fail("invalid schema id:", err)
    }
    s, ok := schemas[id]
    if !ok {
      s = schemaByID(srURL, id)
      schemas[id] = s
    }
    body, err := avro.Marshal(s, toAvroNative(s, parts[2]))
    if err != nil {
      fail("avro encode:", err)
    }
    value := make([]byte, 5, 5+len(body))
    binary.BigEndian.PutUint32(value[1:5], uint32(id))
    value = append(value, body...)
    rec := &kgo.Record{Topic: topic, Value: value, Timestamp: time.UnixMilli(ts)}
    if err := cl.ProduceSync(context.Background(), rec).FirstErr(); err != nil {
      fail("produce failed:", err)
    }
  }
  if err := sc.Err(); err != nil {
    fail("scan failed:", err)
  }
}
EOF
  cd "${repo_root}" && go run "${tmp_go}" "${broker_host}" "${SR_URL}" "${topic}"
}

AVRO_ORDERS_V1='{"type":"record","name":"Order","namespace":"kafkito.e2e","fields":[{"name":"order_id","type":"string"},{"name":"amount_cents","type":"long"}]}'
AVRO_ORDERS_V2='{"type":"record","name":"Order","namespace":"kafkito.e2e","fields":[{"name":"order_id","type":"string"},{"name":"amount_cents","type":"long"},{"name":"currency","type":"string","default":"EUR"},{"name":"note","type":"string","default":""}]}'
JSON_CUSTOMERS_V1='{"$schema":"http://json-schema.org/draft-07/schema#","title":"Customer","type":"object","properties":{"customer_id":{"type":"string"},"email":{"type":"string"}},"required":["customer_id"]}'
PROTO_EVENTS_V1='syntax = "proto3";
package kafkito.e2e;

message Event {
  string event_id = 1;
  int64 occurred_at_ms = 2;
}
'

# seed_schema_registry registers the fixture subjects and fills
# e2e-avro-orders with wire-format records. schemas.spec.ts registers and
# deletes its own subject (e2e-delete-me-value); it is reset here as well so
# an aborted run leaves nothing behind for the next one.
seed_schema_registry() {
  local subject
  for subject in e2e-avro-orders-value e2e-json-customers-value e2e-proto-events-value e2e-delete-me-value; do
    reset_subject "${subject}"
  done

  set_compatibility "e2e-avro-orders-value" "BACKWARD"
  local avro_v1_id avro_v2_id
  avro_v1_id=$(register_schema "e2e-avro-orders-value" "AVRO" "${AVRO_ORDERS_V1}")
  avro_v2_id=$(register_schema "e2e-avro-orders-value" "AVRO" "${AVRO_ORDERS_V2}")

  set_compatibility "e2e-json-customers-value" "FULL"
  register_schema "e2e-json-customers-value" "JSON" "${JSON_CUSTOMERS_V1}" >/dev/null

  set_compatibility "e2e-proto-events-value" "NONE"
  register_schema "e2e-proto-events-value" "PROTOBUF" "${PROTO_EVENTS_V1}" >/dev/null

  local now_ms padding
  now_ms=$(( $(date +%s) * 1000 ))
  # Decodes to ~100 KB of JSON, past the 64 KB list preview, so the record
  # is listed as a truncated preview.
  padding=$(printf '%*s' 100000 '' | tr ' ' 'y')
  {
    printf '%s\t%s\t%s\n' "$((now_ms - 3000))" "${avro_v1_id}" '{"order_id":"E2E-AVRO-1","amount_cents":1999}'
    printf '%s\t%s\t%s\n' "$((now_ms - 2000))" "${avro_v2_id}" '{"order_id":"E2E-AVRO-2","amount_cents":4250,"currency":"USD","note":"second-order"}'
    printf '%s\t%s\t%s\n' "$((now_ms - 1000))" "${avro_v2_id}" "{\"order_id\":\"E2E-AVRO-3\",\"amount_cents\":500,\"currency\":\"EUR\",\"note\":\"${padding}\"}"
  } | produce_avro_records "e2e-avro-orders"
}

leave_group_empty() {
  local topic="$1"
  local group="$2"
  run_in_kafka timeout 5 /opt/kafka/bin/kafka-console-consumer.sh \
    --bootstrap-server "${BROKER_INTERNAL}" --topic "${topic}" \
    --group "${group}" --from-beginning --max-messages 1 \
    >/dev/null 2>&1 || true
}

# produce_large_json puts one JSON record whose value is ~100 KB — well past
# consumer.go's 64 KB truncation boundary (maxMessageValueBytes) but safely
# under both JsonInteractive.tsx's 1 MB interactive-tree size cap and the
# broker's default message.max.bytes, so large-messages.spec.ts exercises the
# real success path (search past 64 KB, click-to-filter, PathSense hydrated
# suggestions), not either size guard's fallback. `_padding` is placed
# *before* `order` so the fields the walk asserts on are guaranteed to start
# past byte 64K — i.e. genuinely inside the truncated-away tail, not merely
# by luck of key ordering.
produce_large_json() {
  local topic="$1"
  local now_ms
  now_ms=$(( $(date +%s) * 1000 ))
  local padding
  padding=$(printf '%*s' 100000 '' | tr ' ' 'y')
  local value
  value="{\"_padding\":\"${padding}\",\"order\":{\"id\":\"E2E-LARGE-1\",\"customer\":{\"name\":\"E2E Tester\",\"email\":\"e2e-tester@example.com\"},\"items\":[{\"sku\":\"SKU-1\",\"price\":19.99,\"qty\":2},{\"sku\":\"E2E-NEEDLE-SKU\",\"price\":42.5,\"qty\":3}],\"notes\":\"e2e-search-needle\"}}"
  printf '%s\t%s\n' "${now_ms}" "${value}" | produce_spread_lines "${topic}"
}

# produce_root_array_json puts one record whose *whole value* is a JSON array
# — a batch of rows per message, a normal Kafka shape. buildPathTree used to
# skip such samples outright, leaving an empty suggestion tree that the UI
# then mislabelled as "sample isn't JSON". `_padding` sits in the first entry
# so the asserted field also lands past the 64 KB truncation boundary,
# covering hydration and the root-array case together.
produce_root_array_json() {
  local topic="$1"
  local now_ms
  now_ms=$(( $(date +%s) * 1000 ))
  local padding
  padding=$(printf '%*s' 100000 '' | tr ' ' 'y')
  local value
  value="[{\"_padding\":\"${padding}\",\"RUNID\":\"E2E-RUN-1\",\"meta\":{\"step\":1}},{\"RUNID\":\"E2E-RUN-2\",\"meta\":{\"step\":2}}]"
  printf '%s\t%s\n' "${now_ms}" "${value}" | produce_spread_lines "${topic}"
}

# produce_large_xml mirrors produce_large_json but with an XML value, to
# cover consumer.go's equivalent truncation-tolerant detection for XML
# (looksXML: first non-whitespace byte is '<') and XPath PathSense's
# hydrated suggestions. Same `_padding` placement rationale as
# produce_large_json: every element/attribute asserted on in
# large-messages.spec.ts starts past byte 64K. The `status` attribute and
# the repeated `<item sku=...>` siblings exist so the spec can assert that
# attributes are suggested as `@name` and that repeated siblings collapse
# onto one path rather than being indexed per position.
produce_large_xml() {
  local topic="$1"
  local now_ms
  now_ms=$(( $(date +%s) * 1000 ))
  local padding
  padding=$(printf '%*s' 100000 '' | tr ' ' 'y')
  local value
  value="<root><_padding>${padding}</_padding><order id=\"E2E-LARGE-XML-1\" status=\"shipped\"><notes>e2e-search-needle-xml</notes><items><item sku=\"XML-SKU-1\"/><item sku=\"XML-SKU-2\"/></items></order></root>"
  printf '%s\t%s\n' "${now_ms}" "${value}" | produce_spread_lines "${topic}"
}

# produce_masked_json puts two records whose `customer.email` the
# data_masking rule in kafkito-e2e.yaml masks: a small one, and a ~100 KB one
# whose email sits past the 64 KB preview boundary (masking.spec.ts).
produce_masked_json() {
  local topic="$1"
  local now_ms
  now_ms=$(( $(date +%s) * 1000 ))
  local padding
  padding=$(printf '%*s' 100000 '' | tr ' ' 'y')
  {
    printf '%s\t%s\n' "$((now_ms - 1000))" '{"order":"E2E-MASK-1","customer":{"email":"hidden-e2e@example.com"}}'
    printf '%s\t%s\n' "${now_ms}" "{\"_padding\":\"${padding}\",\"customer\":{\"email\":\"hidden-large@example.com\"},\"order\":\"E2E-MASK-2\"}"
  } | produce_spread_lines "${topic}"
}

# produce_masked_key_headers puts one record whose key and authorization
# header the data_masking rules in kafkito-e2e.yaml mask; its value and
# trace-id header stay visible (masking.spec.ts).
produce_masked_key_headers() {
  local topic="$1"
  printf '%s\n' 'authorization:Bearer e2e-token-secret,trace-id:e2e-trace-visible|cust-e2e-4711#{"order":"E2E-MASK-KH-1"}' |
    run_in_kafka_stdin /opt/kafka/bin/kafka-console-producer.sh \
      --bootstrap-server "${BROKER_INTERNAL}" --topic "${topic}" \
      --property parse.headers=true --property headers.delimiter='|' \
      --property headers.separator=',' --property headers.key.separator=':' \
      --property parse.key=true --property key.separator='#' >/dev/null 2>&1
}

main() {
  require_tools

  echo "seed: waiting for broker on ${BROKER_INTERNAL} (via ${CONTAINER})"
  wait_for_broker

  echo "seed: recreating fixture topics"
  # 10 days of retention: comfortably beyond the -6d backdated message
  # produce_spread_lines writes below, well past the broker's default
  # 24h KAFKA_LOG_RETENTION_HOURS, so the fixture isn't racing the
  # broker's retention-check cycle for messages that are already
  # "old" the moment they're produced.
  recreate_topic "e2e-walk-target" 4 "retention.ms=864000000"
  recreate_topic "e2e-walk-large" 1
  recreate_topic "e2e-large-message" 1
  recreate_topic "e2e-large-message-xml" 1
  recreate_topic "e2e-root-array" 1
  recreate_topic "e2e-copy-source" 1
  recreate_topic "e2e-copy-dest" 1
  recreate_topic "e2e-produce-target" 1
  recreate_topic "e2e-masked" 1
  recreate_topic "e2e-masked-kh" 1
  recreate_topic "e2e-masked-kh-dest" 1
  recreate_topic "e2e-avro-orders" 1

  echo "seed: producing fixture messages"
  now_ms=$(( $(date +%s) * 1000 ))
  day_ms=$((24 * 60 * 60 * 1000))
  {
    for i in $(seq 1 1); do printf '%s\tseed-message-%s\n' "$((now_ms - 6 * day_ms))" "$i"; done
    for i in $(seq 2 3); do printf '%s\tseed-message-%s\n' "$((now_ms - 4 * day_ms))" "$i"; done
    for i in $(seq 4 6); do printf '%s\tseed-message-%s\n' "$((now_ms - 2 * day_ms))" "$i"; done
    for i in $(seq 7 12); do printf '%s\tseed-message-%s\n' "$((now_ms - 1 * day_ms))" "$i"; done
  } | produce_spread_lines "e2e-walk-target"
  produce_lines "e2e-walk-large" 50
  produce_lines "e2e-copy-source" 1500
  produce_large_json "e2e-large-message"
  produce_large_xml "e2e-large-message-xml"
  produce_root_array_json "e2e-root-array"
  produce_masked_json "e2e-masked"
  produce_masked_key_headers "e2e-masked-kh"

  echo "seed: waiting for schema registry on ${SR_URL}"
  wait_for_schema_registry
  echo "seed: registering fixture subjects and producing Avro records"
  seed_schema_registry

  echo "seed: bringing group e2e-idle-group to Empty"
  leave_group_empty "e2e-walk-target" "e2e-idle-group"

  echo "seed: done"
}

main "$@"
