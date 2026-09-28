// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every route behind the RBAC middleware (the cluster routes) maps to a
// permission in resolvePermission or is listed in rbacExemptRoutes, never
// both. A new route without a mapping fails here instead of being denied
// at runtime.
func TestRBAC_EveryClusterRouteIsMappedOrExempt(t *testing.T) {
	t.Parallel()

	router, ok := recordingServer(t).(chi.Routes)
	require.True(t, ok, "server.New must return a chi router")
	seen := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = normalizeChiRoute(route)
		if !strings.HasPrefix(route, "/api/v1/clusters") {
			return nil
		}
		key := method + " " + route
		seen[key] = true

		var resType, action string
		r := chi.NewRouter()
		r.Method(method, route, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			resType, _, action, _ = resolvePermission(req)
		}))
		path := opParams.Replace(strings.ReplaceAll(route, "{cluster}", "kf"))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))

		reason, exempt := rbacExemptRoutes[key]
		if exempt {
			assert.Empty(t, resType, "%s is exempt but also mapped to %s:%s", key, resType, action)
			assert.NotEmpty(t, reason, "%s: exemption without a reason", key)
		} else {
			assert.NotEmpty(t, resType, "%s has no RBAC mapping in resolvePermission and is not in rbacExemptRoutes", key)
			assert.NotEmpty(t, action, key)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, seen, 36, "cluster routes walked")
	for key := range rbacExemptRoutes {
		assert.True(t, seen[key], "rbacExemptRoutes lists %s, which is not a cluster route", key)
	}
}

// With RBAC on, a route without a mapping is denied with a neutral body and
// a warning that names the method and route pattern, never a header value.
func TestRBACMiddleware_UnmappedRouteIsDeniedAndLogged(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	next := false
	r := chi.NewRouter()
	r.Group(func(g chi.Router) {
		g.Use(rbacMiddleware(policyAllowAll(), log))
		g.Get("/api/v1/clusters/{cluster}/unmapped", func(http.ResponseWriter, *http.Request) { next = true })
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterShared+"/unmapped", nil)
	req.Header.Set(rbacTestHeader, userAdmin)
	req.Header.Set(PrivateClusterHeader, "cHJpdmF0ZS1oZWFkZXItdmFsdWU=")
	req.Header.Set("Authorization", "Bearer secret-token-value")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.JSONEq(t, `{"error":"forbidden","code":"rbac_denied"}`, rec.Body.String())
	assert.False(t, next, "next must not run")

	out := logs.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "method=GET")
	assert.Contains(t, out, "route=/api/v1/clusters/{cluster}/unmapped")
	for _, v := range []string{userAdmin, "cHJpdmF0ZS1oZWFkZXItdmFsdWU=", "secret-token-value", clusterShared} {
		assert.NotContains(t, out, v)
	}
}
