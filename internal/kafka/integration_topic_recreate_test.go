//go:build integration
// +build integration

// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// itProduce produces one text record to partition 0 through the Registry.
func itProduce(ctx context.Context, reg *Registry, topic, value string) (*ProduceResult, error) {
	p := int32(0)
	return reg.Produce(ctx, "it", topic, ProduceRequest{Partition: &p, Value: value})
}

// itValues returns the values of partition 0, oldest first.
func itValues(t *testing.T, ctx context.Context, reg *Registry, topic string) []string {
	t.Helper()
	res, err := reg.ConsumeMessages(ctx, "it", topic, ConsumeOptions{Partition: 0, From: FromStart, Limit: 50, Timeout: 5 * time.Second})
	require.NoError(t, err)
	out := make([]string, 0, len(res.Messages))
	for _, m := range res.Messages {
		out = append(out, m.Value)
	}
	return out
}

// awaitRecreationSeen makes the Registry's client refresh its metadata, so
// it learns the new topic ID of a recreated topic before the next produce.
// Brokers before Kafka 4.1 accept produce requests by topic name, so
// without this the first produce could still reach the new topic before
// the client notices the recreation.
func awaitRecreationSeen(t *testing.T, reg *Registry) {
	t.Helper()
	cl, err := reg.Client("it")
	require.NoError(t, err)
	cl.ForceMetadataRefresh()
	time.Sleep(2 * time.Second)
}

// TestIntegration_TopicRecreate deletes and recreates a topic, through
// kafkito and through another client, and produces to and consumes from
// each new incarnation through the Registry.
func TestIntegration_TopicRecreate(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const topic = "it-recreate"
	create := func() {
		require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{Name: topic, Partitions: 1, ReplicationFactor: 1}))
	}
	create()
	_, err := itProduce(ctx, reg, topic, "first")
	require.NoError(t, err)

	t.Run("through kafkito", func(t *testing.T) {
		require.NoError(t, reg.DeleteTopic(ctx, "it", topic))
		require.Eventually(t, func() bool {
			return reg.CreateTopic(ctx, "it", CreateTopicRequest{Name: topic, Partitions: 1, ReplicationFactor: 1}) == nil
		}, 30*time.Second, 200*time.Millisecond, "recreate topic")
		awaitRecreationSeen(t, reg)

		res, err := itProduce(ctx, reg, topic, "second")
		require.NoError(t, err)
		assert.EqualValues(t, 0, res.Offset)
		assert.Equal(t, []string{"second"}, itValues(t, ctx, reg, topic))
	})

	t.Run("by another client", func(t *testing.T) {
		other, err := kgo.NewClient(kgo.SeedBrokers(broker))
		require.NoError(t, err)
		defer other.Close()
		adm := kadm.NewClient(other)

		_, err = adm.DeleteTopic(ctx, topic)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			resp, err := adm.CreateTopic(ctx, 1, 1, nil, topic)
			return err == nil && resp.Err == nil
		}, 30*time.Second, 200*time.Millisecond, "recreate topic")
		awaitRecreationSeen(t, reg)

		for i := range 3 {
			res, err := itProduce(ctx, reg, topic, fmt.Sprintf("third-%d", i))
			require.NoError(t, err, "produce %d", i)
			assert.EqualValues(t, i, res.Offset)
		}
		assert.Equal(t, []string{"third-0", "third-1", "third-2"}, itValues(t, ctx, reg, topic))

		topics, err := reg.ListTopics(ctx, "it")
		require.NoError(t, err)
		var names []string
		for _, tp := range topics {
			names = append(names, tp.Name)
		}
		assert.Contains(t, names, topic)
	})
}
