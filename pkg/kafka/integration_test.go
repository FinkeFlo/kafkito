//go:build integration
// +build integration

// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

// Package kafka integration tests spin up a real Kafka broker via Testcontainers.
// Run with:
//
//	make test-integration
//
// Requires Docker to be running locally. Tests are skipped when Docker is
// unavailable to keep the path non-blocking on CI runners without Docker.
package kafka

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

// startBroker boots a single-node Kafka (KRaft) container and returns the
// bootstrap address plus a cleanup function. It skips the test if Docker is
// not reachable in the current environment.
func startBroker(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	container, err := tckafka.Run(ctx,
		"confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID("kafkito-it"),
	)
	if err != nil {
		if os.Getenv("KAFKITO_IT_REQUIRE_DOCKER") == "" {
			t.Skipf("skip: docker/testcontainers unavailable: %v", err)
		}
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	brokers, err := container.Brokers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, brokers)
	return brokers[0]
}

// newRegistry builds a Registry pointed at the given bootstrap address.
func newRegistry(t *testing.T, broker string) *Registry {
	t.Helper()
	clusters := []config.ClusterConfig{{
		Name:    "it",
		Brokers: []string{broker},
	}}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	reg := NewRegistry(clusters, logger)
	t.Cleanup(func() { reg.Close() })
	return reg
}

// TestIntegration_TopicLifecycle smoke-tests create/list/describe/delete of a topic.
func TestIntegration_TopicLifecycle(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := "it-topic-lifecycle"
	require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{
		Name:              topic,
		Partitions:        3,
		ReplicationFactor: 1,
	}))

	topics, err := reg.ListTopics(ctx, "it")
	require.NoError(t, err)
	found := false
	for _, tpc := range topics {
		if tpc.Name == topic {
			found = true
			require.Equal(t, 3, tpc.Partitions)
			break
		}
	}
	require.True(t, found, "created topic not listed")

	detail, err := reg.DescribeTopic(ctx, "it", topic)
	require.NoError(t, err)
	require.Equal(t, topic, detail.Name)
	require.Len(t, detail.Partitions, 3)

	require.NoError(t, reg.DeleteTopic(ctx, "it", topic))
}

// TestIntegration_ProduceConsume produces a handful of records and then pulls
// them back via ConsumeMessages.
func TestIntegration_ProduceConsume(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := "it-produce-consume"
	require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{
		Name:              topic,
		Partitions:        1,
		ReplicationFactor: 1,
	}))

	const n = 5
	for i := 0; i < n; i++ {
		res, err := reg.Produce(ctx, "it", topic, ProduceRequest{
			Key:   "k",
			Value: "v",
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, res.Offset, int64(0))
	}

	msgs, err := reg.ConsumeMessages(ctx, "it", topic, ConsumeOptions{
		Partition: -1,
		Limit:     n,
		From:      FromStart,
		Timeout:   8 * time.Second,
	})
	require.NoError(t, err)
	require.Len(t, msgs, n)
	for _, m := range msgs {
		require.Equal(t, "k", m.Key)
		require.Equal(t, "v", m.Value)
	}
}
