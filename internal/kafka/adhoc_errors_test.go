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
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// adhocKfakeRegistry is a Registry with one private cluster, registered
// through UseAdhoc, for a kfake cluster holding topic. Private clusters dial
// through the outbound guard, which refuses loopback, so the registry dials
// kfake directly.
func adhocKfakeRegistry(t *testing.T, topic string) (reg *Registry, name string) {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, topic))
	require.NoError(t, err)
	t.Cleanup(c.Close)
	reg = NewRegistry(nil, slog.New(slog.DiscardHandler))
	t.Cleanup(reg.Close)
	reg.adhocDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	name, err = reg.UseAdhoc(config.ClusterConfig{Brokers: c.ListenAddrs()})
	require.NoError(t, err)
	require.True(t, config.IsAdhocClusterName(name))
	return reg, name
}

// Errors about a private cluster name it __private__, the name its client
// knows it by, and never by its registry name: handlers pass some of these
// texts on to the client and log others.
func TestAdhocClusterErrorsUsePublicName(t *testing.T) {
	t.Parallel()

	reg, name := adhocKfakeRegistry(t, "orders")
	unregistered := config.AdhocClusterPrefix + "0000000000000000"
	// kgo cannot parse the seed broker, so the client cannot be built.
	unusable, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"[::1"}})
	require.NoError(t, err)
	day := int64(24 * time.Hour / time.Millisecond)

	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"client of an unregistered name", func(context.Context) error {
			_, err := reg.Client(unregistered)
			return err
		}},
		{"client of an unusable config", func(context.Context) error {
			_, err := reg.Client(unusable)
			return err
		}},
		{"consume on an unregistered name", func(ctx context.Context) error {
			_, err := reg.ConsumeMessages(ctx, unregistered, "orders", ConsumeOptions{Partition: -1})
			return err
		}},
		{"search on an unregistered name", func(ctx context.Context) error {
			_, err := reg.SearchMessages(ctx, unregistered, "orders", SearchOptions{Partition: -1, Mode: SearchModeContains, Value: "x"})
			return err
		}},
		{"raw value on an unregistered name", func(ctx context.Context) error {
			_, err := reg.FetchRawMessageValue(ctx, unregistered, "orders", 0, 0, RawValueOptions{})
			return err
		}},
		{"consume a missing topic", func(ctx context.Context) error {
			_, err := reg.ConsumeMessages(ctx, name, "missing", ConsumeOptions{Partition: -1})
			return err
		}},
		{"consume a missing partition", func(ctx context.Context) error {
			_, err := reg.ConsumeMessages(ctx, name, "orders", ConsumeOptions{Partition: 7})
			return err
		}},
		{"count a missing topic", func(ctx context.Context) error {
			_, err := reg.CountMessages(ctx, name, "missing", CountMessagesOptions{Partition: -1})
			return err
		}},
		{"timeline of a missing topic", func(ctx context.Context) error {
			_, err := reg.MessageTimeline(ctx, name, "missing", MessageTimelineOptions{Partition: -1, FromTSMs: day, ToTSMs: 2 * day, SlotMs: day})
			return err
		}},
		{"describe a missing group", func(ctx context.Context) error {
			_, err := reg.DescribeGroup(ctx, name, "missing")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := tc.call(ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), config.PrivateClusterSentinel)
			assert.NotContains(t, err.Error(), config.AdhocClusterPrefix)
		})
	}
}
