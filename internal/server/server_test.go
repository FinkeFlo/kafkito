// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both /healthz and /readyz return JSON with `"status":"ok"` when no kafka
// clusters are configured. /readyz additionally emits a `"note"` describing
// the empty-registry state — the per-path assertion captures that distinction.
func TestHealthz_ReturnsOK_PerPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		path        string
		wantNoteSub string
	}{
		{name: "healthz", path: "/healthz", wantNoteSub: ""},
		{name: "readyz", path: "/readyz", wantNoteSub: "no kafka clusters configured"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{Version: "test", Logger: slog.Default()})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			var body struct {
				Status string `json:"status"`
				Note   string `json:"note"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body=%s", rec.Body.String())

			assert.Equal(t, "ok", body.Status, "%s status field", tc.path)
			if tc.wantNoteSub != "" {
				assert.Contains(t, body.Note, tc.wantNoteSub,
					"%s note field must describe empty-registry state", tc.path)
			}
		})
	}
}

func TestInfo_ReturnsConfiguredVersion_AsJSON(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "1.2.3", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var body struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body=%s", rec.Body.String())

	assert.Equal(t, "1.2.3", body.Version)
	assert.Equal(t, "kafkito", body.Name)
}

func TestAPINotFound_ReturnsJSONContentType(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "x", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestSPAFallback_ServesHTML_ForUnknownClientRoute(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "x", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "SPA fallback must serve 200")
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"),
		"Content-Type = %q, want text/html prefix", rec.Header().Get("Content-Type"))
}

// Per-path subtests are intentional: when the embed.FS sibling-build race
// bites in CI, the failure ID names the specific path that 500'd, not a
// collapsed "TestSPAFallbackForDeepRoutes failed" without context.
func TestSPAFallback_ServesHTML_ForDeepClientRoutes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{name: "topics_foo_messages", path: "/topics/foo/messages"},
		{name: "topics_long_uppercase_name_messages", path: "/topics/FRA_aspire_eXtend_SalesPrices_PRD/messages"},
		{name: "clusters_PROD", path: "/clusters/PROD"},
		{name: "settings_clusters", path: "/settings/clusters"},
		{name: "groups", path: "/groups"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{Version: "x", Logger: slog.Default()})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code,
				"%s: SPA fallback must serve 200, body=%s", tc.path, rec.Body.String())
			assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"),
				"%s: Content-Type = %q, want text/html prefix", tc.path, rec.Header().Get("Content-Type"))
		})
	}
}

func TestSPAFallback_RejectsNonIdempotentVerbs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
	}{
		{name: "POST", method: http.MethodPost},
		{name: "PUT", method: http.MethodPut},
		{name: "PATCH", method: http.MethodPatch},
		{name: "DELETE", method: http.MethodDelete},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{Version: "x", Logger: slog.Default()})
			req := httptest.NewRequest(tc.method, "/topics/foo/messages", nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.NotEqual(t, http.StatusOK, rec.Code,
				"%s /topics/foo/messages must not return 200", tc.method)
			assert.False(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"),
				"%s /topics/foo/messages: Content-Type = %q, must not be HTML",
				tc.method, rec.Header().Get("Content-Type"))
		})
	}
}

func TestBackendPrefixes_NeverFallThroughToSPAShell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{name: "api_nope", path: "/api/nope"},
		{name: "api_v1_does_not_exist", path: "/api/v1/does/not/exist"},
		{name: "rpc_unknown_service", path: "/rpc/some.unknown.Service/Method"},
		{name: "user_api_unknown", path: "/user-api/unknown"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{Version: "x", Logger: slog.Default()})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.False(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"),
				"%s: Content-Type = %q, want JSON (not SPA HTML)",
				tc.path, rec.Header().Get("Content-Type"))
			assert.NotEqual(t, http.StatusOK, rec.Code,
				"%s: backend prefix must not return 200", tc.path)
		})
	}
}

func TestMissingAsset_Returns404_NotSPAShell(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "x", Logger: slog.Default()})
	req := httptest.NewRequest(http.MethodGet, "/assets/this-asset-does-not-exist.js", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.False(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"),
		"Content-Type = %q, want non-HTML", rec.Header().Get("Content-Type"))
}
