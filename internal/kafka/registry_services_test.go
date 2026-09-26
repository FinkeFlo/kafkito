// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// TestNewRegistry_ServicesShareConnections pins that every service works on
// the same connection set: an ad-hoc cluster registered through the
// registry is visible to each service, and each service shares one client
// per cluster.
func TestNewRegistry_ServicesShareConnections(t *testing.T) {
	t.Parallel()
	reg := NewRegistry([]config.ClusterConfig{{Name: "c1", Brokers: []string{"127.0.0.1:1"}}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(reg.Close)

	for name, conns := range map[string]*Connections{
		"topics":   reg.Topics.Connections,
		"groups":   reg.Groups.Connections,
		"messages": reg.Messages.Connections,
		"security": reg.Security.Connections,
		"clusters": reg.Clusters.Connections,
	} {
		assert.Same(t, reg.Connections, conns, name)
	}
	assert.Same(t, reg.Clusters, reg.stats, "topics read metrics from the registry's collector")

	name, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"127.0.0.1:2"}})
	require.NoError(t, err)
	for svc, lookup := range map[string]func(string) (config.ClusterConfig, bool){
		"topics":   reg.Topics.ConfigFor,
		"groups":   reg.Groups.ConfigFor,
		"messages": reg.Messages.ConfigFor,
		"security": reg.Security.ConfigFor,
		"clusters": reg.Clusters.ConfigFor,
	} {
		_, ok := lookup(name)
		assert.True(t, ok, "%s sees the ad-hoc cluster", svc)
	}

	topicsClient, err := reg.Topics.Client("c1")
	require.NoError(t, err)
	messagesClient, err := reg.Messages.Client("c1")
	require.NoError(t, err)
	assert.Same(t, topicsClient, messagesClient)
}

// TestRegistryClose_StopsMetricsAndClients pins that Registry.Close still
// stops the collector owned by Clusters and drops the clients owned by
// Connections.
func TestRegistryClose_StopsMetricsAndClients(t *testing.T) {
	t.Parallel()
	reg := NewRegistry([]config.ClusterConfig{{Name: "c1", Brokers: []string{"127.0.0.1:1"}}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := reg.Client("c1")
	require.NoError(t, err)
	reg.StartMetrics(context.Background(), time.Hour)
	mc := reg.metricsCollector()
	require.NotNil(t, mc)

	reg.Close()

	assert.Nil(t, reg.metricsCollector())
	assert.False(t, mc.running.Load())
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.clients)
}
