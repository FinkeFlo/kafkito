#!/usr/bin/env bash
# Seed the local Kafka broker (kafkito-kafka container, started by
# `docker compose up -d kafka`) with the deterministic fixture state the
# Playwright walks need:
#
#   topic e2e-walk-target  4 partitions, 12 messages
#   topic e2e-walk-large   1 partition, 50 messages (Delete-Records walk)
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
  run_in_kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${BROKER_INTERNAL}" --delete --topic "${name}" \
    >/dev/null 2>&1 || true
  run_in_kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${BROKER_INTERNAL}" --create --if-not-exists \
    --topic "${name}" --partitions "${partitions}" --replication-factor 1 \
    >/dev/null
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

main() {
  echo "seed: waiting for broker on ${BROKER_INTERNAL} (via ${CONTAINER})"
  wait_for_broker

  echo "seed: recreating fixture topics"
  recreate_topic "e2e-walk-target" 4
  recreate_topic "e2e-walk-large" 1

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

  echo "seed: bringing group e2e-idle-group to Empty"
  leave_group_empty "e2e-walk-target" "e2e-idle-group"

  echo "seed: done"
}

main "$@"
