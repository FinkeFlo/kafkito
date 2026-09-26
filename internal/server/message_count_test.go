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

func TestCountMessages_RouteIsRegistered_AndReturnsJSON(t *testing.T) {
	t.Parallel()

	var got kafkapkg.CountMessagesOptions
	h := fakeServer(t, stores{messages: fakeMessages{count: func(opts kafkapkg.CountMessagesOptions) (*kafkapkg.MessageCountResult, error) {
		got = opts
		return &kafkapkg.MessageCountResult{TotalApproxCount: 7, Partitions: []kafkapkg.PartitionMessageCount{{Partition: 0, FromOffset: 3, ToOffset: 10, ApproxCount: 7}}}, nil
	}}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/count?from_ts_ms=1000", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.NotEqualf(t, http.StatusNotFound, rec.Code,
		"route not registered (404). body=%s", rec.Body.String())
	var raw map[string]json.RawMessage
	require.NoErrorf(t, json.Unmarshal(rec.Body.Bytes(), &raw),
		"response is not valid JSON. body=%s", rec.Body.String())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"cluster":"test","topic":"orders","total_approx_count":7,"partitions":[{"partition":0,"from_offset":3,"to_offset":10,"approx_count":7}]}`, rec.Body.String())
	assert.Equal(t, kafkapkg.CountMessagesOptions{Partition: -1, FromTSMs: 1000, Timeout: 6 * time.Second}, got)
}

func TestCountMessages_ReturnsNotFound_WhenClusterMissing(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	h := New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/does-not-exist/topics/orders/messages/count", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCountMessages_ReturnsBadRequest_WhenPartitionIsNonNumeric(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/count?partition=abc", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
