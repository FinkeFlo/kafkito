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
#   consumer group e2e-idle-group  in Empty state (consumed once, then exited)
#
# Idempotent: safe to re-run; topics are recreated, the consumer is run
# briefly to bring the group back to Empty.

set -euo pipefail

BROKER_INTERNAL="kafka:9092"
CONTAINER="${KAFKITO_E2E_KAFKA_CONTAINER:-kafkito-kafka}"

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
# under both json-interactive.tsx's 1 MB interactive-tree size cap and the
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

main() {
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

  echo "seed: bringing group e2e-idle-group to Empty"
  leave_group_empty "e2e-walk-target" "e2e-idle-group"

  echo "seed: done"
}

main "$@"
