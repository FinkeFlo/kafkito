// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// The tests in this file pin that a topic which is deleted and recreated
// under the same name stays usable through the Registry. franz-go refuses
// the new incarnation of a topic on a client that already produced to the
// old one (the topic ID changed), so the long-lived per-cluster client has
// to forget the old topic.

const recreatedTopic = "recreated"

// produceText produces one text record through the Registry and returns
// the error, if any.
func produceText(t *testing.T, env *kfakeEnv, value string) (*ProduceResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := int32(0)
	return env.reg.Produce(ctx, kfakeCluster, env.topic, ProduceRequest{Partition: &p, Value: value})
}

// consumeValues returns the values of every record on partition 0, oldest
// first.
func consumeValues(t *testing.T, env *kfakeEnv) []string {
	t.Helper()
	res := consumePage(t, env, ConsumeOptions{Partition: 0, From: FromStart, Limit: 50})
	out := make([]string, 0, len(res.Messages))
	for _, m := range res.Messages {
		out = append(out, m.Value)
	}
	return out
}

// requireTopicListed asserts that ListTopics reports the topic.
func requireTopicListed(t *testing.T, env *kfakeEnv) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	topics, err := env.reg.ListTopics(ctx, kfakeCluster)
	require.NoError(t, err)
	for _, tp := range topics {
		if tp.Name == env.topic {
			return
		}
	}
	t.Fatalf("topic %q not listed", env.topic)
}

// Deleting and recreating a topic through kafkito and then producing to it
// works, and the new topic only holds the new record.
func TestRegistry_TopicRecreatedThroughKafkitoStaysUsable(t *testing.T) {
	env := newKfakeEnv(t, recreatedTopic, 1, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := produceText(t, env, "before")
	require.NoError(t, err)
	require.Equal(t, []string{"before"}, consumeValues(t, env))

	require.NoError(t, env.reg.DeleteTopic(ctx, kfakeCluster, recreatedTopic))
	require.NoError(t, env.reg.CreateTopic(ctx, kfakeCluster, CreateTopicRequest{Name: recreatedTopic, Partitions: 1}))

	// DeleteTopic already purged the topic, so the produce does not first
	// run into the stale topic ID, which costs one metadata refresh
	// (MetadataMinAge, 5s by default) before the client fails the record.
	started := time.Now()
	res, err := produceText(t, env, "after")
	require.NoError(t, err)
	assert.Less(t, time.Since(started), 3*time.Second, "produce must not wait for the stale topic ID to fail")
	assert.EqualValues(t, 0, res.Offset, "the record must land in the new topic")
	requireTopicListed(t, env)
	assert.Equal(t, []string{"after"}, consumeValues(t, env))
}

// A topic that another tool deletes and recreates is usable too: the
// produce that runs into the stale topic ID recovers on its own.
func TestRegistry_TopicRecreatedExternallyStaysUsable(t *testing.T) {
	env := newKfakeEnv(t, recreatedTopic, 1, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := produceText(t, env, "before")
	require.NoError(t, err)

	adm := kadm.NewClient(env.cl)
	_, err = adm.DeleteTopic(ctx, recreatedTopic)
	require.NoError(t, err)
	_, err = adm.CreateTopic(ctx, 1, 1, nil, recreatedTopic)
	require.NoError(t, err)

	res, err := produceText(t, env, "after")
	require.NoError(t, err)
	assert.EqualValues(t, 0, res.Offset, "the record must land in the new topic")

	// A second produce must not need the recovery again.
	res, err = produceText(t, env, "again")
	require.NoError(t, err)
	assert.EqualValues(t, 1, res.Offset)

	requireTopicListed(t, env)
	assert.Equal(t, []string{"after", "again"}, consumeValues(t, env))
}

// ProduceBatch recovers the same way; every record of the batch lands in
// the new topic exactly once.
func TestRegistry_ProduceBatchToExternallyRecreatedTopic(t *testing.T) {
	env := newKfakeEnv(t, recreatedTopic, 1, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := produceText(t, env, "before")
	require.NoError(t, err)

	adm := kadm.NewClient(env.cl)
	_, err = adm.DeleteTopic(ctx, recreatedTopic)
	require.NoError(t, err)
	_, err = adm.CreateTopic(ctx, 1, 1, nil, recreatedTopic)
	require.NoError(t, err)

	p := int32(0)
	n, err := env.reg.ProduceBatch(ctx, kfakeCluster, recreatedTopic, []ProduceRequest{
		{Partition: &p, Value: "a"}, {Partition: &p, Value: "b"}, {Partition: &p, Value: "c"},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, consumeValues(t, env))
}

// Private clusters get their own long-lived client from the same options
// plus a dial guard; recreating a topic works for them as well. The guard
// refuses loopback addresses such as kfake's, so the test swaps only the
// dialer of the private cluster's client.
func TestRegistry_TopicRecreatedOnPrivateCluster(t *testing.T) {
	env := newKfakeEnv(t, recreatedTopic, 1, nil)
	name, err := env.reg.UseAdhoc(config.ClusterConfig{Brokers: env.brokers})
	require.NoError(t, err)
	cfg, ok := env.reg.ConfigFor(name)
	require.True(t, ok)
	cl, err := kgo.NewClient(append(clientOpts(cfg, slog.New(slog.DiscardHandler)),
		kgo.Dialer((&net.Dialer{Timeout: 10 * time.Second}).DialContext))...)
	require.NoError(t, err)
	env.reg.mu.Lock()
	env.reg.clients[name] = cl
	env.reg.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := int32(0)
	produce := func(v string) (*ProduceResult, error) {
		return env.reg.Produce(ctx, name, recreatedTopic, ProduceRequest{Partition: &p, Value: v})
	}
	_, err = produce("before")
	require.NoError(t, err)

	// Through kafkito.
	require.NoError(t, env.reg.DeleteTopic(ctx, name, recreatedTopic))
	require.NoError(t, env.reg.CreateTopic(ctx, name, CreateTopicRequest{Name: recreatedTopic, Partitions: 1}))
	res, err := produce("after-kafkito")
	require.NoError(t, err)
	assert.EqualValues(t, 0, res.Offset)

	// By another client.
	adm := kadm.NewClient(env.cl)
	_, err = adm.DeleteTopic(ctx, recreatedTopic)
	require.NoError(t, err)
	_, err = adm.CreateTopic(ctx, 1, 1, nil, recreatedTopic)
	require.NoError(t, err)
	res, err = produce("after-other")
	require.NoError(t, err)
	assert.EqualValues(t, 0, res.Offset)
}
