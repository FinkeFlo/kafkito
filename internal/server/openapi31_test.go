// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// The spec is OpenAPI 3.1 and uses `const` (HealthResponse.status) and
// `type: [T, "null"]` (MeResponse.scopes/roles, ClusterConfig.data_masking).
// These tests pin how the generated code and the validator handle them.

func TestOpenAPI31_GeneratedTypes(t *testing.T) {
	t.Parallel()

	t.Run("const becomes a single-value enum", func(t *testing.T) {
		t.Parallel()
		assert.True(t, gen.HealthResponseStatusOk.Valid())
		assert.Equal(t, "ok", string(gen.HealthResponseStatusOk))
		assert.False(t, gen.HealthResponseStatus("OK").Valid())
	})

	t.Run("nullable array is a pointer serialised as null", func(t *testing.T) {
		t.Parallel()
		b, err := json.Marshal(gen.MeResponse{Permissions: map[string][]string{}})
		require.NoError(t, err)
		assert.Contains(t, string(b), `"scopes":null`)
		assert.Contains(t, string(b), `"roles":null`)

		scopes := []string{"a"}
		b, err = json.Marshal(gen.MeResponse{Scopes: &scopes, Permissions: map[string][]string{}})
		require.NoError(t, err)
		assert.Contains(t, string(b), `"scopes":["a"]`)
	})

	t.Run("nullable array maps to the config type", func(t *testing.T) {
		t.Parallel()
		var cfg gen.ClusterConfig
		require.NoError(t, json.Unmarshal([]byte(`{"brokers":["h:1"],"data_masking":null}`), &cfg))
		assert.Nil(t, cfg.DataMasking)
		require.NoError(t, json.Unmarshal([]byte(`{"brokers":["h:1"],"data_masking":[{"topics":["t"],"fields":["$.x"]}]}`), &cfg))
		require.Len(t, cfg.DataMasking, 1)
		assert.Equal(t, []string{"t"}, cfg.DataMasking[0].Topics)
	})
}

// ClusterConfig.data_masking is `type: [array, "null"]`: the request
// validator accepts null and an array and rejects any other type. The body
// then fails the SSRF policy in Go (loopback broker), which proves it passed
// the validator.
func TestOpenAPI31_RequestValidation_NullableArray(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: config.Defaults()})

	for _, tc := range []struct {
		value     string
		validator bool // rejected by the request validator
	}{
		{`null`, false},
		{`[]`, false},
		{`[{"topics":["t"],"fields":["$.x"]}]`, false},
		{`"x"`, true},
		{`{}`, true},
		{`[1]`, true},
	} {
		body := `{"brokers":["127.0.0.1:9092"],"data_masking":` + tc.value + `}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, tc.value)
		if tc.validator {
			assert.Contains(t, rec.Body.String(), `"code":"invalid_request"`, tc.value)
			assert.Contains(t, rec.Body.String(), `/data_masking`, tc.value)
		} else {
			assert.Contains(t, rec.Body.String(), `broker \"127.0.0.1:9092\"`, tc.value)
		}
	}
}

// Response validation (used by the contract tests) understands `const` and
// nullable arrays in OpenAPI 3.1.
func TestOpenAPI31_ResponseValidation(t *testing.T) {
	t.Parallel()

	router := contractRouter(t)
	validate := func(path, body string) error {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		route, params, err := router.FindRoute(req)
		require.NoError(t, err)
		in := &openapi3filter.ResponseValidationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route},
			Status:                 http.StatusOK,
			Header:                 http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"r-1"}},
		}
		in.SetBodyBytes([]byte(body))
		return openapi3filter.ValidateResponse(context.Background(), in)
	}

	require.NoError(t, validate("/healthz", `{"status":"ok"}`))
	require.Error(t, validate("/healthz", `{"status":"OK"}`), "const")

	me := func(scopes string) string {
		return `{"user":"","email":"","tenant":"","scopes":` + scopes + `,"roles":null,"permissions":{},"anonymous":true,"jwt":false,"rbac_enabled":false}`
	}
	require.NoError(t, validate("/api/v1/me", me(`null`)))
	require.NoError(t, validate("/api/v1/me", me(`["a"]`)))
	require.Error(t, validate("/api/v1/me", me(`"a"`)), "nullable array")
}
