// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func TestMessageTimeline_RouteIsRegistered_AndReturnsJSON(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline?from_ts_ms=1000&to_ts_ms=2000&bucket_ms=500", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.NotEqualf(t, http.StatusNotFound, rec.Code,
		"route not registered (404). body=%s", rec.Body.String())
	var raw map[string]json.RawMessage
	assert.NoErrorf(t, json.Unmarshal(rec.Body.Bytes(), &raw),
		"response is not valid JSON. body=%s", rec.Body.String())
}

func TestMessageTimeline_ReturnsNotFound_WhenClusterMissing(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	h := New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/does-not-exist/topics/orders/messages/timeline?from_ts_ms=1000&to_ts_ms=2000&bucket_ms=500", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMessageTimeline_ReturnsBadRequest_WhenMissingRequiredParams(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMessageTimeline_ReturnsBadRequest_WhenPartitionIsNonNumeric(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline?partition=abc&from_ts_ms=1000&to_ts_ms=2000&bucket_ms=500", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMessageTimeline_ReturnsBadRequest_WhenTooManyBuckets(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline?from_ts_ms=0&to_ts_ms=100000&bucket_ms=1", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
