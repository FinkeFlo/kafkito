// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

func TestInjectKafkitoProduceHeaders(t *testing.T) {
	t.Parallel()

	t.Run("initializes headers and sets source", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{}
		injectKafkitoProduceHeaders(&req, "")

		require.NotNil(t, req.Headers)
		assert.Equal(t, "true", req.Headers["X-Kafkito-Source"])
		_, hasUser := req.Headers["X-Kafkito-User"]
		assert.False(t, hasUser)
	})

	t.Run("preserves caller headers and adds metadata", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{
			Headers: map[string]string{
				"X-Custom": "value",
			},
		}
		injectKafkitoProduceHeaders(&req, "alice")

		assert.Equal(t, "value", req.Headers["X-Custom"])
		assert.Equal(t, "true", req.Headers["X-Kafkito-Source"])
		assert.Equal(t, "alice", req.Headers["X-Kafkito-User"])
	})

	t.Run("overwrites spoofed metadata headers", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{
			Headers: map[string]string{
				"X-Kafkito-Source": "false",
				"X-Kafkito-User":   "spoofed-user",
			},
		}
		injectKafkitoProduceHeaders(&req, "real-user")

		assert.Equal(t, "true", req.Headers["X-Kafkito-Source"])
		assert.Equal(t, "real-user", req.Headers["X-Kafkito-User"])
	})
}

// TestProduceMessage_OversizedBodyReturns413 guards the fix for the Replay
// dialog silently uploading a full recovered value that the server would
// reject: without the http.MaxBytesError -> 413 mapping in produceMessage,
// an over-cap body surfaces as a generic 400 "invalid body" instead of a
// clearly-labeled, actionable size error. No live broker is needed: the
// MaxBytesReader trips during JSON decode, before any Kafka client call.
func TestProduceMessage_OversizedBodyReturns413(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry([]config.ClusterConfig{
		{Name: "test", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
	}, slog.Default())
	h := New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{},
	})

	oversized := strings.Repeat("a", maxProduceBodyBytes+1)
	body, err := json.Marshal(map[string]string{"key": "k", "value": oversized, "value_encoding": "text"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test/topics/orders/messages", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "produce limit")
}
