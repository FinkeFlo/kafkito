// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// lockedBuffer is a bytes.Buffer for a logger that kgo's goroutines share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Log lines about a private cluster name it by config.ClusterLogName, never
// by its registry name, in the json and the text log format.
func TestAdhocClusterLogsUseLogName(t *testing.T) {
	t.Parallel()

	insecureTLS := config.ClusterConfig{
		Brokers: []string{"192.0.2.1:9092"},
		TLS:     config.TLSConfig{Enabled: true, InsecureSkipVerify: true},
	}
	cases := []struct {
		name    string
		wantMsg string
		// run makes reg log wantMsg and returns the registry name of the
		// cluster the line is about.
		run func(t *testing.T, c *kfake.Cluster, reg *Registry, name string, log *slog.Logger) string
	}{
		{
			name: "topic configs", wantMsg: "describe topic configs failed",
			run: func(t *testing.T, c *kfake.Cluster, reg *Registry, name string, _ *slog.Logger) string {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c.ControlKey(int16(kmsg.DescribeConfigs), func(kmsg.Request) (kmsg.Response, error, bool) {
					cancel()
					return nil, errors.New("describe configs refused"), true
				})
				_, _ = reg.DescribeTopic(ctx, name, "orders")
				return name
			},
		},
		{
			name: "client with TLS verification disabled", wantMsg: "TLS verification disabled for cluster",
			run: func(t *testing.T, _ *kfake.Cluster, reg *Registry, _ string, _ *slog.Logger) string {
				insecure, err := reg.UseAdhoc(insecureTLS)
				require.NoError(t, err)
				_, err = reg.Client(insecure)
				require.NoError(t, err)
				return insecure
			},
		},
		{
			name: "scan client with TLS verification disabled", wantMsg: "TLS verification disabled for cluster",
			run: func(t *testing.T, _ *kfake.Cluster, reg *Registry, _ string, _ *slog.Logger) string {
				insecure, err := reg.UseAdhoc(insecureTLS)
				require.NoError(t, err)
				cfg, ok := reg.ConfigFor(insecure)
				require.True(t, ok)
				cl, err := reg.scanClient(recordScan{cluster: insecure, topic: "orders", role: "consume", cfg: cfg},
					map[int32]*scanCursor{0: {}})
				require.NoError(t, err)
				cl.Close()
				return insecure
			},
		},
		{
			name: "metrics refresh", wantMsg: "metrics: admin unavailable",
			run: func(_ *testing.T, _ *kfake.Cluster, reg *Registry, _ string, log *slog.Logger) string {
				unregistered := config.AdhocClusterPrefix + "0000000000000000"
				mc := &metricsCollector{
					log: log, conns: reg.Connections, ctx: context.Background(),
					states: map[string]*clusterState{unregistered: {prev: map[string]topicSample{}}},
				}
				mc.refreshOne(unregistered)
				return unregistered
			},
		},
	}
	formats := []struct {
		name    string
		handler func(io.Writer) slog.Handler
		attr    func(key, value string) string
	}{
		{
			name: "json",
			handler: func(w io.Writer) slog.Handler {
				return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
			},
			attr: func(key, value string) string { return fmt.Sprintf("%q:%q", key, value) },
		},
		{
			name: "text",
			handler: func(w io.Writer) slog.Handler {
				return slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
			},
			attr: func(key, value string) string { return key + "=" + value },
		},
	}
	for _, format := range formats {
		for _, tc := range cases {
			t.Run(format.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
				require.NoError(t, err)
				t.Cleanup(c.Close)
				var logs lockedBuffer
				log := slog.New(format.handler(&logs))
				reg, name := adhocRegistryFor(t, c, log)

				about := tc.run(t, c, reg, name, log)
				reg.Close()

				logged := logs.String()
				assert.NotContains(t, logged, config.AdhocClusterPrefix, "the registry name is never logged")
				var lines []string
				for line := range strings.SplitSeq(logged, "\n") {
					if strings.Contains(line, tc.wantMsg) {
						lines = append(lines, line)
					}
				}
				require.NotEmpty(t, lines, "no %q line in:\n%s", tc.wantMsg, logged)
				for _, line := range lines {
					assert.Contains(t, line, format.attr("cluster", config.ClusterLogName(about)))
				}
			})
		}
	}
}
