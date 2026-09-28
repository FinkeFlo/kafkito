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

		assert.Equal(t, map[string]string{
			"X-Kafkito-Source": "true",
			"X-Kafkito-User":   "real-user",
		}, req.Headers)
	})

	t.Run("drops a spoofed user when there is no identity", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{
			Headers: map[string]string{"X-Kafkito-User": "spoofed-user"},
		}
		injectKafkitoProduceHeaders(&req, "")

		assert.Equal(t, map[string]string{"X-Kafkito-Source": "true"}, req.Headers)
	})

	t.Run("drops every reserved key regardless of case or map", func(t *testing.T) {
		t.Parallel()

		req := kafkapkg.ProduceRequest{
			Headers: map[string]string{
				"x-kafkito-user":   "lower",
				"X-KAFKITO-USER":   "upper",
				"x-Kafkito-Source": "false",
				"X-Kafkito-Other":  "anything",
				"trace-id":         "keep",
			},
			HeadersB64: map[string]string{
				"X-Kafkito-User": "c3Bvb2Zl",
				"x-kafkito-user": "c3Bvb2Zl",
				"bin":            "3q2+7w==",
			},
		}
		injectKafkitoProduceHeaders(&req, "alice")

		assert.Equal(t, map[string]string{
			"trace-id":         "keep",
			"X-Kafkito-Source": "true",
			"X-Kafkito-User":   "alice",
		}, req.Headers)
		assert.Equal(t, map[string]string{"bin": "3q2+7w=="}, req.HeadersB64)
	})

	t.Run("leaves the caller's maps untouched", func(t *testing.T) {
		t.Parallel()

		headers := map[string]string{"X-Kafkito-User": "spoofed", "keep": "v"}
		headersB64 := map[string]string{"x-kafkito-user": "c3Bvb2Zl"}
		req := kafkapkg.ProduceRequest{Headers: headers, HeadersB64: headersB64}
		injectKafkitoProduceHeaders(&req, "alice")

		assert.Equal(t, map[string]string{"X-Kafkito-User": "spoofed", "keep": "v"}, headers)
		assert.Equal(t, map[string]string{"x-kafkito-user": "c3Bvb2Zl"}, headersB64)
		assert.Nil(t, req.HeadersB64, "an all-reserved HeadersB64 must not leave an empty map behind")
	})
}

func TestIsReservedHeader(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key  string
		want bool
	}{
		{"X-Kafkito-User", true},
		{"x-kafkito-user", true},
		{"X-KAFKITO-SOURCE", true},
		{"X-Kafkito-", true},
		{"x-kafkito-anything", true},
		{"X-\u212Aafkito-User", true}, // Kelvin sign, folds to "k"
		{"X-Kafkito", false},
		{"X-Kafkitos-User", false},
		{"Kafkito-User", false},
		{" X-Kafkito-User", false},
		{"trace-id", false},
		{"", false},
		{"X-Kafk\xffito-User", false},
	} {
		assert.Equal(t, tc.want, isReservedHeader(tc.key), "key %q", tc.key)
	}
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
