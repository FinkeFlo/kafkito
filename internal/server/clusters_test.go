// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func TestIsACLClientErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "required field missing", msg: "principal required", want: true},
		{name: "validate keyword", msg: "validate: host must not be empty", want: true},
		{name: "resource_type keyword", msg: "unknown resource_type: Foo", want: true},
		{name: "pattern_type keyword", msg: "invalid pattern_type: Bar", want: true},
		{name: "operation keyword", msg: "unsupported operation: WRITE", want: true},
		{name: "permission_type keyword", msg: "unknown permission_type: DENY", want: true},
		{name: "broker network error", msg: "dial tcp broker-host:9092: connection refused", want: false},
		{name: "leader not available", msg: "leader not available", want: false},
		{name: "timeout", msg: "context deadline exceeded", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isACLClientErr(tc.msg), "isACLClientErr(%q)", tc.msg)
		})
	}
}

func TestIsSCRAMClientErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "required field missing", msg: "user required", want: true},
		{name: "mechanism keyword", msg: "unsupported mechanism: PLAIN", want: true},
		{name: "iterations keyword", msg: "iterations must be >= 4096", want: true},
		{name: "broker network error", msg: "dial tcp broker-host:9092: connection refused", want: false},
		{name: "timeout", msg: "context deadline exceeded", want: false},
		{name: "sasl auth failure", msg: "SASL authentication failed", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isSCRAMClientErr(tc.msg), "isSCRAMClientErr(%q)", tc.msg)
		})
	}
}

func TestIsSearchClientErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "jsonpath keyword", msg: "invalid jsonpath expression: $.[", want: true},
		{name: "xpath keyword", msg: "xpath compile error", want: true},
		{name: "regex keyword", msg: "invalid regex: [unclosed", want: true},
		{name: "numeric op keyword", msg: "numeric op requires a number value", want: true},
		{name: "unknown search mode", msg: "unknown search mode: fuzzy", want: true},
		{name: "unknown operator", msg: "unknown operator: between", want: true},
		{name: "js filter keyword", msg: "js filter compile error", want: true},
		{name: "broker network error", msg: "dial tcp broker-host:9092: connection refused", want: false},
		{name: "timeout", msg: "context deadline exceeded", want: false},
		{name: "topic not found", msg: "topic does not exist", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isSearchClientErr(tc.msg), "isSearchClientErr(%q)", tc.msg)
		})
	}
}

// TestCreateGroupHandler exercises POST /clusters/{cluster}/groups.
//
// a.reg is the concrete *kafka.Registry (not an interface), and no live
// Kafka broker is available in this unit-test environment (Docker is not
// available here). kafka.Registry.CreateGroup validates the request body
// (group_id/topic/strategy) before ever calling Admin(cluster), and
// Admin(cluster) itself fails fast with ErrUnknownCluster for a cluster
// that isn't configured -- neither of those paths touches a broker. The
// happy-path (200) and already-exists (409) cases both require a reachable
// broker (CreateGroup calls adm.ListGroups then commits offsets), so they
// are NOT exercised here; see the doc comment on newSampleTestHandler for
// the same limitation applied to /sample.
func TestCreateGroupHandler(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsBadRequest_WhenGroupIDMissing", func(t *testing.T) {
		t.Parallel()

		h := newSampleTestHandler(t)
		body := `{"topic":"t","strategy":"latest"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test/groups", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
	})

	t.Run("ReturnsNotFound_WhenClusterMissing", func(t *testing.T) {
		t.Parallel()

		reg := kafkapkg.NewRegistry(nil, slog.Default())
		h := New(Options{
			Version:  "test",
			Logger:   slog.Default(),
			Registry: reg,
			Config:   config.Config{},
		})
		body := `{"group_id":"g","topic":"t","strategy":"latest"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/does-not-exist/groups", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), "unknown cluster")
	})
}
