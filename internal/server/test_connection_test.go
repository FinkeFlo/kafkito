// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// unreachableBroker is in TEST-NET-1 (RFC 5737): it passes the SSRF
// pre-check but nothing ever answers, so a ping only ends at its deadline.
const unreachableBroker = "192.0.2.1:9092"

func TestTestCluster_UsesConfiguredTimeout(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
	h := New(Options{Version: "x", Logger: slog.Default(), Registry: reg, Config: cfg})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, config.ClusterConfig{
		Brokers: []string{unreachableBroker},
	}))
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var info kafkapkg.ClusterInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.False(t, info.Reachable)
	// The 15s default would blow well past this bound.
	assert.Less(t, elapsed, 5*time.Second, "probe must stop at the configured 300ms budget")
}
