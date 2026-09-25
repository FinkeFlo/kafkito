// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/FinkeFlo/kafkito/api"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenAPISpec_MatchesRouter is the cheap route/spec parity guard from
// ADR-0005: api/openapi.yaml must be a valid document and describe exactly
// the method+path pairs the chi router registers — no more, no less.
//
// Scope: every route reachable through server.New with a Registry, including
// /healthz and /readyz (both are in the spec). Excluded are only routes that
// are not part of kafkito's own contract:
//   - /user-api/*: dev-only stub (devauth build tag) standing in for the
//     approuter's endpoint; production never serves it from Go.
//   - chi's catch-all NotFound/MethodNotAllowed handlers and the SPA
//     fallback, which chi.Walk does not report as routes anyway.
func TestOpenAPISpec_MatchesRouter(t *testing.T) {
	t.Parallel()

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(api.Spec)
	require.NoError(t, err, "api/openapi.yaml must parse")
	require.NoError(t, doc.Validate(loader.Context), "api/openapi.yaml must validate")

	specOps := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			specOps[strings.ToUpper(method)+" "+path] = true
		}
	}

	h := New(Options{
		Version:  "test",
		Logger:   slog.Default(),
		Registry: kafkapkg.NewRegistry(nil, slog.Default()),
	})
	router, ok := h.(chi.Routes)
	require.True(t, ok, "server.New must return a chi router")

	routeOps := map[string]bool{}
	err = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = normalizeChiRoute(route)
		if strings.HasPrefix(route, "/user-api/") {
			return nil
		}
		routeOps[method+" "+route] = true
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, routeOps)

	assert.Empty(t, missing(routeOps, specOps), "routes registered in chi but missing from api/openapi.yaml")
	assert.Empty(t, missing(specOps, routeOps), "operations in api/openapi.yaml without a chi route")
}

// TestOpenAPISpec_OperationsHaveIDs keeps generated client code stable: every
// operation needs a unique operationId.
func TestOpenAPISpec_OperationsHaveIDs(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	require.NoError(t, err)
	seen := map[string]string{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			where := method + " " + path
			if !assert.NotEmpty(t, op.OperationID, "%s has no operationId", where) {
				continue
			}
			if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %q used by both %s and %s", op.OperationID, prev, where)
			}
			seen[op.OperationID] = where
		}
	}
}

var chiRegexParam = regexp.MustCompile(`\{([^}:]+):[^}]*\}`)

// normalizeChiRoute converts chi patterns to OpenAPI path templates: drops
// regexp constraints ({id:[0-9]+} → {id}), trailing wildcards and a trailing
// slash that chi.Walk reports for mounted sub-router roots.
func normalizeChiRoute(route string) string {
	route = chiRegexParam.ReplaceAllString(route, "{$1}")
	route = strings.TrimSuffix(route, "/*")
	if len(route) > 1 {
		route = strings.TrimSuffix(route, "/")
	}
	return route
}

func missing(have, in map[string]bool) []string {
	var out []string
	for k := range have {
		if !in[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
