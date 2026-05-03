// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

// TestTopicConsumersUnknownClusterReturns404 ensures the route is mounted and
// reports a 404 with a useful body when the cluster is not configured (rather
// than 500 or a silent route-miss).
func TestTopicConsumersUnknownClusterReturns404(t *testing.T) {
	t.Parallel()

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

	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "unknown cluster")
}
