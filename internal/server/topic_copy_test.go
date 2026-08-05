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

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func TestProduceEncodingFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		rendered     string
		b64          string
		encoding     string
		wantValue    string
		wantEncoding string
		wantOK       bool
	}{
		// "null" must stay on "text": decodeProducePayload turns an empty
		// "text" value into nil, which is exactly what a null key/value is.
		{name: "null", encoding: "null", wantValue: "", wantEncoding: "text", wantOK: true},
		// "empty" must NOT use "text", or the zero-length payload collapses to
		// nil and the copy writes a tombstone (and repartitions empty keys).
		{name: "empty", encoding: "empty", wantValue: "", wantEncoding: "empty", wantOK: true},
		{name: "text", rendered: "hello", encoding: "text", wantValue: "hello", wantEncoding: "text", wantOK: true},
		{name: "json", rendered: `{"a":1}`, encoding: "json", wantValue: `{"a":1}`, wantEncoding: "text", wantOK: true},
		{name: "binary", rendered: "0xdeadbeef", b64: "3q2+7w==", encoding: "binary", wantValue: "3q2+7w==", wantEncoding: "base64", wantOK: true},
		{name: "avro", rendered: `{"a":1}`, encoding: "avro", wantOK: false},
		{name: "json_schema", rendered: `{"a":1}`, encoding: "json_schema", wantOK: false},
		{name: "protobuf", rendered: `{"a":1}`, encoding: "protobuf", wantOK: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value, encoding, ok := produceEncodingFor(tc.rendered, tc.b64, tc.encoding)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantValue, value)
				assert.Equal(t, tc.wantEncoding, encoding)
			}
		})
	}
}

// TestCopyProduceRequest covers the per-record copy decision without a broker:
// which records are reproducible, how their payload encodings are carried over,
// and that building the produce request leaves the consumed message untouched.
func TestCopyProduceRequest(t *testing.T) {
	t.Parallel()

	t.Run("masked_records_are_skipped", func(t *testing.T) {
		t.Parallel()
		msg := kafkapkg.Message{
			Partition: 1, Offset: 7,
			Value: "***", ValueEncoding: "text",
			Masked: true,
		}
		_, ok := copyProduceRequest(msg, false, "alice")
		assert.False(t, ok, "a redacted value must never be produced to the destination")
	})

	t.Run("schema_registry_records_are_skipped", func(t *testing.T) {
		t.Parallel()
		msg := kafkapkg.Message{
			Value: `{"a":1}`, ValueEncoding: "avro",
		}
		_, ok := copyProduceRequest(msg, false, "alice")
		assert.False(t, ok, "avro payloads lose their wire-format bytes and cannot be reproduced")
	})

	t.Run("zero_length_value_keeps_the_empty_encoding", func(t *testing.T) {
		t.Parallel()
		msg := kafkapkg.Message{
			Key: "", KeyEncoding: "empty",
			Value: "", ValueEncoding: "empty",
		}
		got, ok := copyProduceRequest(msg, false, "")
		require.True(t, ok)
		assert.Equal(t, "empty", got.KeyEncoding)
		assert.Equal(t, "empty", got.ValueEncoding)
	})

	t.Run("binary_headers_travel_as_base64", func(t *testing.T) {
		t.Parallel()
		msg := kafkapkg.Message{
			Value: "v", ValueEncoding: "text",
			// recordToMessage renders a non-UTF-8 header value as display hex
			// in Headers and keeps the raw bytes in HeadersB64.
			Headers:    map[string]string{"trace": "0xdeadbeef"},
			HeadersB64: map[string]string{"trace": "3q2+7w=="},
		}
		got, ok := copyProduceRequest(msg, false, "")
		require.True(t, ok)
		assert.Equal(t, "3q2+7w==", got.HeadersB64["trace"],
			"without HeadersB64 the destination gets the ASCII text 0xdeadbeef")
	})

	t.Run("provenance_headers_do_not_mutate_the_source_message", func(t *testing.T) {
		t.Parallel()
		msg := kafkapkg.Message{
			Value: "v", ValueEncoding: "text",
			Headers: map[string]string{"origin": "keep-me"},
		}
		got, ok := copyProduceRequest(msg, false, "alice")
		require.True(t, ok)

		assert.Equal(t, "keep-me", got.Headers["origin"])
		assert.Equal(t, "true", got.Headers["X-Kafkito-Source"])
		assert.Equal(t, "alice", got.Headers["X-Kafkito-User"])

		// The produce request must own its header map: injecting into
		// msg.Headers would rewrite the record we just consumed.
		assert.Equal(t, map[string]string{"origin": "keep-me"}, msg.Headers)
	})

	t.Run("preserve_partition_pins_the_destination_partition", func(t *testing.T) {
		t.Parallel()
		msg := kafkapkg.Message{Partition: 3, Value: "v", ValueEncoding: "text"}

		with, ok := copyProduceRequest(msg, true, "")
		require.True(t, ok)
		require.NotNil(t, with.Partition)
		assert.Equal(t, int32(3), *with.Partition)

		without, ok := copyProduceRequest(msg, false, "")
		require.True(t, ok)
		assert.Nil(t, without.Partition)
	})
}

func TestHighestPartition(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(-1), highestPartition(nil))
	assert.Equal(t, int32(4), highestPartition([]kafkapkg.PartitionInfo{
		{Partition: 0}, {Partition: 4}, {Partition: 2},
	}))
}

// TestCheckDestPartitions pins the off-by-one in the preserve_partition
// pre-flight check: N destination partitions cover source indexes 0..N-1.
func TestCheckDestPartitions(t *testing.T) {
	t.Parallel()

	assert.NoError(t, checkDestPartitions("dst", 3, 2))
	assert.NoError(t, checkDestPartitions("dst", 6, 2))
	assert.NoError(t, checkDestPartitions("dst", 1, -1), "no source partitions: nothing to check")

	err := checkDestPartitions("dst", 2, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has 2 partition(s)")
	assert.Contains(t, err.Error(), "at least 3")
}

// newCopyTestHandler builds a full handler (RBAC + private-cluster
// middleware wired exactly as in production) with the given clusters and
// RBAC config. No real Kafka broker is available in unit tests, so
// consume/produce calls fail; these tests cover validation, prod
// confirmation, and RBAC dispatch, which all happen before any broker call.
func newCopyTestHandler(t *testing.T, clusters []config.ClusterConfig, rbacCfg config.RBACConfig) http.Handler {
	t.Helper()
	reg := kafkapkg.NewRegistry(clusters, slog.Default())
	return New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: reg,
		Config:   config.Config{RBAC: rbacCfg},
	})
}

func TestCopyMessages_ValidationErrors(t *testing.T) {
	t.Parallel()

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "test", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
	}, config.RBACConfig{})

	cases := []struct {
		name          string
		body          string
		wantErrSubstr string
	}{
		{
			name:          "missing_dest_topic",
			body:          `{"dest_cluster":"test"}`,
			wantErrSubstr: "dest_topic is required",
		},
		{
			name:          "neither_dest_cluster_nor_config",
			body:          `{"dest_topic":"orders2"}`,
			wantErrSubstr: "dest_cluster or dest_cluster_config is required",
		},
		{
			name: "both_dest_cluster_and_config",
			body: `{"dest_topic":"orders2","dest_cluster":"test",` +
				`"dest_cluster_config":{"name":"x","brokers":["127.0.0.1:9092"],"auth":{"type":"none"}}}`,
			wantErrSubstr: "mutually exclusive",
		},
		{
			name:          "self_copy_same_cluster_and_topic",
			body:          `{"dest_cluster":"test","dest_topic":"orders"}`,
			wantErrSubstr: "must differ from the source",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test/topics/orders/copy", bytes.NewReader([]byte(tc.body)))
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			var body map[string]string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "raw=%s", rec.Body.String())
			assert.Contains(t, body["error"], tc.wantErrSubstr)
		})
	}
}

func TestCopyMessages_RequiresProdConfirmation_ForDestination(t *testing.T) {
	t.Parallel()

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
		{Name: "dst-prod", Brokers: []string{"127.0.0.1:19093"}, Auth: config.AuthConfig{Type: "none"}, IsProd: true},
	}, config.RBACConfig{})

	body := `{"dest_cluster":"dst-prod","dest_topic":"orders2"}`

	t.Run("without_confirmation_header_returns_428", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusPreconditionRequired, rec.Code, "body=%s", rec.Body.String())
	})

	t.Run("with_confirmation_header_passes_the_gate", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
		req.Header.Set(ProdConfirmHeader, "true")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		// No live broker: the handler streams SSE and reports a consume
		// error in the body, but it must get past the 428 gate to do so —
		// proven by the response being a 200 SSE stream, not a 428.
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), "data: ")
	})
}

func TestCopyMessages_DestinationRequiresProducePermission(t *testing.T) {
	t.Parallel()

	rbacCfg := config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		Roles: []config.RoleConfig{
			{
				Name: "consumer",
				Permissions: []config.PermissionConfig{
					{Resource: "topic:*", Actions: []string{"consume"}},
				},
			},
		},
		Subjects: []config.SubjectConfig{
			{User: userMallory, Roles: []string{"consumer"}},
		},
	}

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
		{Name: "dst", Brokers: []string{"127.0.0.1:19093"}, Auth: config.AuthConfig{Type: "none"}},
	}, rbacCfg)

	body := `{"dest_cluster":"dst","dest_topic":"orders2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
	req.Header.Set(rbacTestHeader, userMallory)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	var respBody map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &respBody), "raw=%s", rec.Body.String())
	assert.Equal(t, "forbidden", respBody["error"])
	assert.Equal(t, "topic:orders2", respBody["resource"])
	assert.Equal(t, "produce", respBody["action"])
}

// TestCopyMessages_SourceConsumePermissionEnforcedByMiddleware guards against
// resolvePermission losing its case for the copy route again: without it,
// rbacMiddleware short-circuits with no check at all and this deny-all
// policy would let the request straight through instead of 403ing.
func TestCopyMessages_SourceConsumePermissionEnforcedByMiddleware(t *testing.T) {
	t.Parallel()

	rbacCfg := config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		// No roles/subjects: every action is denied.
	}

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
		{Name: "dst", Brokers: []string{"127.0.0.1:19093"}, Auth: config.AuthConfig{Type: "none"}},
	}, rbacCfg)

	body := `{"dest_cluster":"dst","dest_topic":"orders2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
	req.Header.Set(rbacTestHeader, userMallory)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	var respBody map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &respBody), "raw=%s", rec.Body.String())
	assert.Equal(t, "topic:orders", respBody["resource"])
	assert.Equal(t, "consume", respBody["action"])
}

// TestCopyMessages_UnknownDestClusterIsRejectedBeforeStreaming covers the
// destination pre-flight check on the one path that needs no broker: an
// unconfigured cluster fails in the registry itself. It must be a plain 400,
// not an error event inside a 200 stream.
func TestCopyMessages_UnknownDestClusterIsRejectedBeforeStreaming(t *testing.T) {
	t.Parallel()

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
	}, config.RBACConfig{})

	body := `{"dest_cluster":"nope","dest_topic":"orders2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
	var respBody map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &respBody), "raw=%s", rec.Body.String())
	assert.Contains(t, respBody["error"], "unknown dest_cluster")
}

// TestCopyMessages_EmitsInitialEventBeforeCopying guards defect 5: the client
// must hear from the stream before the first (multi-second) consume call, both
// so the UI can leave its pending state and so a client that is already gone is
// detected before any broker work. Without the initial event the first bytes on
// the wire would be the terminal consume-error event.
func TestCopyMessages_EmitsInitialEventBeforeCopying(t *testing.T) {
	t.Parallel()

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
		{Name: "dst", Brokers: []string{"127.0.0.1:19093"}, Auth: config.AuthConfig{Type: "none"}},
	}, config.RBACConfig{})

	body := `{"dest_cluster":"dst","dest_topic":"orders2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.True(t, strings.HasPrefix(rec.Body.String(), "data: {\"copied\":0}\n\n"),
		"stream must open with a zero-progress event, got %q", rec.Body.String())
}

// TestCopyMessages_ConcurrencyLimit fills the copy semaphore and asserts the
// 429 shape. Deliberately NOT parallel: it holds every slot for the duration,
// and Go runs sequential top-level tests to completion before any parallel one,
// so no other copy test can be starved by it.
func TestCopyMessages_ConcurrencyLimit(t *testing.T) {
	held := 0
	for tryAcquireCopySlot() {
		held++
	}
	require.Equal(t, maxConcurrentCopies, held, "semaphore should start empty")
	t.Cleanup(func() {
		for i := 0; i < held; i++ {
			releaseCopySlot()
		}
	})

	h := newCopyTestHandler(t, []config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}, Auth: config.AuthConfig{Type: "none"}},
		{Name: "dst", Brokers: []string{"127.0.0.1:19093"}, Auth: config.AuthConfig{Type: "none"}},
	}, config.RBACConfig{})

	body := `{"dest_cluster":"dst","dest_topic":"orders2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/src/topics/orders/copy", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "30", rec.Header().Get("Retry-After"))
	var respBody map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &respBody), "raw=%s", rec.Body.String())
	assert.Equal(t, "copy_concurrency_limit", respBody["code"])
	assert.Contains(t, respBody["error"], "too many concurrent copy jobs")
}
