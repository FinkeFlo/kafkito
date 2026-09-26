// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"errors"
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

// newSampleTestHandler serves a fake message store that fails the test if a
// handler calls it: the tests using it cover route registration and
// parameter validation, which must reject a request before any Kafka call.
func newSampleTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return fakeServer(t, stores{messages: fakeMessages{
		consume: func(kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
			t.Error("unexpected ConsumeMessages call")
			return nil, errors.New("unexpected call")
		},
		count: func(kafkapkg.CountMessagesOptions) (*kafkapkg.MessageCountResult, error) {
			t.Error("unexpected CountMessages call")
			return nil, errors.New("unexpected call")
		},
		timeline: func(kafkapkg.MessageTimelineOptions) (*kafkapkg.MessageTimelineResult, error) {
			t.Error("unexpected MessageTimeline call")
			return nil, errors.New("unexpected call")
		},
	}})
}

// TestSampleMessages_RouteIsRegistered_AndReturnsJSON verifies the /sample
// handler is registered, asks the store for the newest five messages of all
// partitions and returns them with the cluster/topic echo and sampled_at.
func TestSampleMessages_RouteIsRegistered_AndReturnsJSON(t *testing.T) {
	t.Parallel()

	var got kafkapkg.ConsumeOptions
	h := fakeServer(t, stores{messages: fakeMessages{consume: func(opts kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
		got = opts
		return &kafkapkg.ConsumeResult{Messages: []kafkapkg.Message{{Partition: 0, Offset: 41, Value: `{"id":1}`, ValueEncoding: "json", KeyEncoding: "empty"}}}, nil
	}}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/sample", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.NotEqualf(t, http.StatusNotFound, rec.Code,
		"route not registered (404). body=%s", rec.Body.String())
	var raw map[string]json.RawMessage
	require.NoErrorf(t, json.Unmarshal(rec.Body.Bytes(), &raw),
		"response is not valid JSON. body=%s", rec.Body.String())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `"test"`, string(raw["cluster"]))
	assert.JSONEq(t, `"orders"`, string(raw["topic"]))
	assert.Contains(t, raw, "sampled_at")
	var msgs []kafkapkg.Message
	require.NoError(t, json.Unmarshal(raw["messages"], &msgs))
	require.Len(t, msgs, 1)
	assert.EqualValues(t, 41, msgs[0].Offset)
	assert.Equal(t, kafkapkg.ConsumeOptions{Partition: -1, Limit: 5, From: kafkapkg.FromEnd, Timeout: 6 * time.Second}, got)
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
