// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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

// TestSampleMessagesDefaults verifies the /sample handler is registered, accepts
// no query params, and returns a structured JSON body (cluster + topic fields).
// With no real Kafka it will return 502 from the broker call, which still proves
// the route resolves and the handler runs. An empty messages slice (no Kafka)
// satisfies len <= 5.
//
// TestSampleMessagesDefaults is limited: with no in-process Kafka mock,
// ConsumeMessages always returns a connection error and the handler responds
// with 502, so we cannot assert the 200-path response shape (cluster/topic
// echo, sampled_at presence, len(messages) <= 5). When a Kafka mock or
// integration harness becomes available, extend this test to assert those
// fields. The clamping behaviour for n is fully covered by
// TestParseSampleQueryCapsN; defaults by TestParseSampleQueryDefaults.
func TestSampleMessagesDefaults(t *testing.T) {
	h := newSampleTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/sample", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Route must be registered — anything other than 404 proves the handler ran.
	if rec.Code == http.StatusNotFound {
		t.Fatalf("route not found (404); handler not registered. body=%s", rec.Body.String())
	}
	// With a non-reachable broker we get 502. Verify the body is valid JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v; body=%s", err, rec.Body.String())
	}
}

// TestSampleMessagesUnknownCluster returns 404 for unknown clusters.
func TestSampleMessagesUnknownCluster(t *testing.T) {
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

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestSampleMessagesInvalidN rejects non-numeric n.
func TestSampleMessagesInvalidN(t *testing.T) {
	h := newSampleTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/sample?n=abc", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
