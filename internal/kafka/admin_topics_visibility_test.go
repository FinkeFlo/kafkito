// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Brokers learn about a created topic asynchronously, so a list right after
// CreateTopic can miss it. The UI refetches the topic list as soon as the
// create returns; CreateTopic must not return before metadata shows the topic.
func TestCreateTopic_WaitsUntilMetadataListsTheTopic(t *testing.T) {
	const topic = "fresh-topic"
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "existing"))
	require.NoError(t, err)
	t.Cleanup(c.Close)

	host, portStr, err := net.SplitHostPort(c.ListenAddrs()[0])
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	// After the create, the next metadata answers still report the topic as
	// unknown, like a broker that has not applied the controller's record yet.
	var mu sync.Mutex
	created := false
	lagging := 3
	c.ControlKey(int16(kmsg.CreateTopics), func(kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		mu.Lock()
		created = true
		mu.Unlock()
		return nil, nil, false
	})
	c.ControlKey(int16(kmsg.Metadata), func(req kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		mreq := req.(*kmsg.MetadataRequest)
		mu.Lock()
		defer mu.Unlock()
		if !created || lagging == 0 || !asksFor(mreq, topic) {
			return nil, nil, false
		}
		lagging--
		resp := mreq.ResponseKind().(*kmsg.MetadataResponse)
		resp.ControllerID = 0
		b := kmsg.NewMetadataResponseBroker()
		b.NodeID, b.Host, b.Port = 0, host, int32(port)
		resp.Brokers = append(resp.Brokers, b)
		mt := kmsg.NewMetadataResponseTopic()
		mt.Topic = kmsg.StringPtr(topic)
		mt.ErrorCode = kerr.UnknownTopicOrPartition.Code
		resp.Topics = append(resp.Topics, mt)
		return resp, nil, true
	})

	env := kfakeEnvFor(t, c, "existing", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, env.reg.CreateTopic(ctx, kfakeCluster, CreateTopicRequest{Name: topic, Partitions: 1, ReplicationFactor: 1}))

	topics, err := env.reg.ListTopics(ctx, kfakeCluster)
	require.NoError(t, err)
	names := make([]string, 0, len(topics))
	for _, ti := range topics {
		names = append(names, ti.Name)
	}
	require.Contains(t, names, topic)
}

// kadm lists topics from the client's metadata cache, so a list that ran
// shortly before the create would otherwise be served again without the
// new topic.
func TestCreateTopic_ListRightAfterCreateBypassesStaleCache(t *testing.T) {
	env := newKfakeEnv(t, "existing", 1, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := env.reg.ListTopics(ctx, kfakeCluster)
	require.NoError(t, err)
	require.NoError(t, env.reg.CreateTopic(ctx, kfakeCluster, CreateTopicRequest{Name: "fresh-topic", Partitions: 1, ReplicationFactor: 1}))

	topics, err := env.reg.ListTopics(ctx, kfakeCluster)
	require.NoError(t, err)
	names := make([]string, 0, len(topics))
	for _, ti := range topics {
		names = append(names, ti.Name)
	}
	require.Contains(t, names, "fresh-topic")
}

func asksFor(req *kmsg.MetadataRequest, topic string) bool {
	if req.Topics == nil {
		return true
	}
	for _, t := range req.Topics {
		if t.Topic != nil && *t.Topic == topic {
			return true
		}
	}
	return false
}
