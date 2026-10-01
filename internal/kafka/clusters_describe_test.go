// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/connerr"
)

// closedPort returns a loopback address nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// jsonLogLines returns the JSON log records in logs whose msg is msg.
func jsonLogLines(t *testing.T, logs, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

// An unreachable configured cluster is reported by the class of its ping
// error and the class's fixed text, never by the error itself, which names
// the broker address. The full error is logged once, not on every call.
func TestDescribe_UnreachableClusterGetsClass(t *testing.T) {
	t.Parallel()

	addr := closedPort(t)
	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	logs := &lockedBuffer{}
	reg := NewRegistry([]config.ClusterConfig{{Name: "down", Brokers: []string{addr}}},
		slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(reg.Close)

	for range 2 {
		infos := reg.Describe(t.Context(), 2*time.Second)
		require.Len(t, infos, 1)
		info := infos[0]
		assert.False(t, info.Reachable)
		assert.Equal(t, connerr.Refused, info.ErrorClass)
		assert.Equal(t, "connection refused", info.Error)
		for _, raw := range []string{host, port, "dial tcp", "connect:", "unable to dial"} {
			assert.NotContains(t, info.Error, raw)
		}
	}

	lines := jsonLogLines(t, logs.String(), "cluster not reachable")
	require.Len(t, lines, 1, "logged when the failure starts, not per call:\n%s", logs.String())
	assert.Equal(t, "WARN", lines[0]["level"])
	assert.Equal(t, "down", lines[0]["cluster"])
	assert.Equal(t, string(connerr.Refused), lines[0]["error_class"])
	assert.Contains(t, lines[0]["err"], addr, "operators get the full error")
}

// notePing logs a failure when it starts and when its class changes, and
// the recovery once. A failure because the caller went away is ignored.
func TestNotePing_LogsChangesOnly(t *testing.T) {
	t.Parallel()

	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	timeout := context.DeadlineExceeded
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	steps := []struct {
		ctx     context.Context
		cluster string
		err     error
		wantMsg string
	}{
		{nil, "a", nil, ""},
		{nil, "a", refused, "cluster not reachable"},
		{nil, "a", refused, ""},
		{nil, "b", refused, "cluster not reachable"},
		{nil, "a", timeout, "cluster not reachable"},
		{nil, "a", nil, "cluster reachable again"},
		{nil, "a", nil, ""},
		{gone, "a", context.Canceled, ""},
		{nil, "a", nil, ""},
		{nil, "a", refused, "cluster not reachable"},
	}
	logs := &lockedBuffer{}
	reg := NewRegistry(nil, slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(reg.Close)

	for i, step := range steps {
		ctx := step.ctx
		if ctx == nil {
			ctx = t.Context()
		}
		before := len(logs.String())
		reg.notePing(ctx, step.cluster, step.err)
		added := strings.TrimSpace(logs.String()[before:])
		if step.wantMsg == "" {
			assert.Empty(t, added, "step %d", i)
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(added), &rec), "step %d: one line, got %q", i, added)
		assert.Equal(t, step.wantMsg, rec["msg"], "step %d", i)
		assert.Equal(t, step.cluster, rec["cluster"], "step %d", i)
	}
}
