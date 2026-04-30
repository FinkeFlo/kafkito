// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

// TestTopicConsumersUnknownClusterReturns404 ensures the new route is mounted
// and reports a 404 when the cluster is not configured (rather than 500 or a
// silent route-miss).
func TestTopicConsumersUnknownClusterReturns404(t *testing.T) {
	reg := kafkapkg.NewRegistry(nil, slog.Default())
	h := New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/missing/topics/orders/consumers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown cluster") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
