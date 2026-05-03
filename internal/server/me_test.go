// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/server"
	"github.com/FinkeFlo/kafkito/pkg/config"
)

type stubValidator struct{ p *auth.Principal }

func (s stubValidator) Validate(context.Context, string) (*auth.Principal, error) {
	return s.p, nil
}

func TestHandleMe_WithJWT_PopulatesPrincipalFieldsInResponse(t *testing.T) {
	t.Parallel()

	v := stubValidator{p: &auth.Principal{
		Subject:  "u-1",
		UserName: "testuser",
		Email:    "f@x",
		Scopes:   []string{"Display"},
		Tenant:   "test-zone",
	}}
	h := server.New(server.Options{Version: "test", Config: config.Config{}, Auth: v})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer ignored-by-stub")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body=%s", rec.Body.String())

	t.Run("echoes_user_name", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "testuser", body["user"])
	})

	t.Run("marks_jwt_true", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, true, body["jwt"])
	})

	t.Run("echoes_tenant_from_principal", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "test-zone", body["tenant"])
	})

	t.Run("echoes_scopes_array", func(t *testing.T) {
		t.Parallel()
		scopes, ok := body["scopes"].([]any)
		require.True(t, ok, "scopes is not a JSON array: %T", body["scopes"])
		require.Len(t, scopes, 1)
		assert.Equal(t, "Display", scopes[0])
	})
}

func TestHandleMe_WithoutJWT_FallsThroughToHeaderTrust(t *testing.T) {
	t.Parallel()

	// No Auth wired -> Principal is absent -> falls through to header trust path.
	h := server.New(server.Options{Version: "test", Config: config.Config{}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body=%s", rec.Body.String())
	assert.Equal(t, false, body["jwt"])
}
