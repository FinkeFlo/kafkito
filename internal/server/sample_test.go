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

// newSampleTestHandler builds a handler with a "test" cluster registered so
// the RBAC + route machinery resolves properly. Because no real Kafka broker
// is available in unit tests, ConsumeMessages will fail and return 502 for
// the happy path; the tests below therefore cover route registration, param
// validation, and unknown-cluster behaviour.
func newSampleTestHandler(t *testing.T) http.Handler {
	t.Helper()
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{
		{Name: "test", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
	}, slog.Default())
	return New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{},
	})
}

// TestSampleMessages_RouteIsRegistered_AndReturnsJSON verifies the /sample
// handler is registered and returns a structured JSON body. With no real
// Kafka the broker call returns 502, which still proves the route resolves
// and the handler runs.
//
// Phase-2 follow-up: with no in-process Kafka mock, ConsumeMessages always
// returns a connection error and the handler responds with 502, so we cannot
// assert the 200-path response shape (cluster/topic echo, sampled_at presence,
// len(messages) <= 5). When a kfake-backed fixture lands, extend this test to
// assert those fields. The clamping behaviour for n is fully covered by
// TestParseSampleQueryCapsN; defaults by TestParseSampleQueryDefaults.
func TestSampleMessages_RouteIsRegistered_AndReturnsJSON(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/sample", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.NotEqualf(t, http.StatusNotFound, rec.Code,
		"route not registered (404). body=%s", rec.Body.String())
	var raw map[string]json.RawMessage
	assert.NoErrorf(t, json.Unmarshal(rec.Body.Bytes(), &raw),
		"response is not valid JSON. body=%s", rec.Body.String())
}

func TestSampleMessages_ReturnsNotFound_WhenClusterMissing(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	h := New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/does-not-exist/topics/orders/sample", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSampleMessages_ReturnsBadRequest_WhenNIsNonNumeric(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/sample?n=abc", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
