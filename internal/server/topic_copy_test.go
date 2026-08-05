// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
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
		{name: "null", encoding: "null", wantValue: "", wantEncoding: "text", wantOK: true},
		{name: "empty", encoding: "empty", wantValue: "", wantEncoding: "text", wantOK: true},
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
