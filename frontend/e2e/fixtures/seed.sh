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
  produce_lines "e2e-walk-target" 12
  produce_lines "e2e-walk-large" 50

  echo "seed: bringing group e2e-idle-group to Empty"
  leave_group_empty "e2e-walk-target" "e2e-idle-group"

  echo "seed: done"
}

main "$@"
