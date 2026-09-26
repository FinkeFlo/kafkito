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

func TestMessageTimeline_RouteIsRegistered_AndReturnsJSON(t *testing.T) {
	t.Parallel()

	var got kafkapkg.MessageTimelineOptions
	h := fakeServer(t, stores{messages: fakeMessages{timeline: func(opts kafkapkg.MessageTimelineOptions) (*kafkapkg.MessageTimelineResult, error) {
		got = opts
		return &kafkapkg.MessageTimelineResult{FromTSMs: opts.FromTSMs, ToTSMs: opts.ToTSMs, SlotMs: opts.SlotMs, Slots: []kafkapkg.TimelineSlot{
			{FromTSMs: 1000, ToTSMs: 1500, ApproxCount: 2},
			{FromTSMs: 1500, ToTSMs: 2000, ApproxCount: 0},
		}}, nil
	}}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline?from_ts_ms=1000&to_ts_ms=2000&slot_ms=500", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.NotEqualf(t, http.StatusNotFound, rec.Code,
		"route not registered (404). body=%s", rec.Body.String())
	var raw map[string]json.RawMessage
	require.NoErrorf(t, json.Unmarshal(rec.Body.Bytes(), &raw),
		"response is not valid JSON. body=%s", rec.Body.String())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"cluster":"test","topic":"orders","from_ts_ms":1000,"to_ts_ms":2000,"slot_ms":500,"slots":[{"from_ts_ms":1000,"to_ts_ms":1500,"approx_count":2},{"from_ts_ms":1500,"to_ts_ms":2000,"approx_count":0}]}`, rec.Body.String())
	assert.Equal(t, kafkapkg.MessageTimelineOptions{Partition: -1, FromTSMs: 1000, ToTSMs: 2000, SlotMs: 500, Timeout: 20 * time.Second}, got)
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/does-not-exist/topics/orders/messages/timeline?from_ts_ms=1000&to_ts_ms=2000&slot_ms=500", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline?partition=abc&from_ts_ms=1000&to_ts_ms=2000&slot_ms=500", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMessageTimeline_ReturnsBadRequest_WhenTooManySlots(t *testing.T) {
	t.Parallel()

	h := newSampleTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/topics/orders/messages/timeline?from_ts_ms=0&to_ts_ms=100000&slot_ms=1", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
