// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/connerr"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// The cluster list and /readyz report a configured cluster that cannot be
// reached by the class of the failure and its fixed text, not by the dial
// error, which names the broker address. The log has the full error once.
func TestClusterStatus_UnreachableClusterIsReportedByClass(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "down", Brokers: []string{addr}}}, logger)
	t.Cleanup(reg.Close)
	h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: config.Defaults()})
	router := contractRouter(t)

	for _, tc := range []struct {
		path       string
		wantStatus int
	}{
		{"/api/v1/clusters", http.StatusOK},
		{"/readyz", http.StatusServiceUnavailable},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, tc.wantStatus, rec.Code, "%s: %s", tc.path, rec.Body.String())
		assertResponseMatchesSpec(t, router, req, rec)

		var body struct {
			Clusters []kafkapkg.ClusterInfo `json:"clusters"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), tc.path)
		require.Len(t, body.Clusters, 1, tc.path)
		info := body.Clusters[0]
		assert.False(t, info.Reachable, tc.path)
		assert.Equal(t, connerr.Refused, info.ErrorClass, tc.path)
		assert.Equal(t, "connection refused", info.Error, tc.path)
		for _, raw := range []string{"127.0.0.1", port, "dial tcp", "connect:", "unable to dial"} {
			assert.NotContains(t, rec.Body.String(), raw, tc.path)
		}
	}

	lines := logLinesWith(logs.String(), "cluster not reachable")
	require.Len(t, lines, 1, "logged when the failure starts, not per request:\n%s", logs.String())
	assert.Contains(t, lines[0], addr, "operators get the full error")
}
