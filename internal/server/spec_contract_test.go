// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// specOperationCount is the number of operations in api/openapi.yaml. A new
// operation changes it on purpose.
const specOperationCount = 41

// strictOperationHeader carries the operation id recorded by
// recordingServer's strict middleware.
const strictOperationHeader = "X-Test-Strict-Operation"

// recordingServer is server.New whose generated handlers never run: a
// strict middleware answers 299 and names the operation the request was
// bound to, so a request that gets there went through the generated
// binding of exactly that operation.
func recordingServer(t *testing.T) http.Handler {
	t.Helper()
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "kf", Brokers: []string{unreachableBroker}}}, slog.Default())
	t.Cleanup(reg.Close)
	record := func(_ gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		return func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ any) (any, error) {
			w.Header().Set(strictOperationHeader, operationID)
			w.WriteHeader(299)
			return nil, nil
		}
	}
	return New(Options{
		Version:           "test",
		Logger:            slog.Default(),
		Registry:          reg,
		Config:            config.Defaults(),
		strictMiddlewares: []gen.StrictMiddlewareFunc{record},
	})
}

// strictRequest is a request to the route pattern of op with path
// parameters, query and body that pass the request validator.
func strictRequest(method, pattern, id string) *http.Request {
	path := opParams.Replace(strings.ReplaceAll(pattern, "{cluster}", "kf")) + validQuery(id)
	var req *http.Request
	if body := validBody(id); body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	return req
}

// pascal is the Go name oapi-codegen gives an operation id.
func pascal(id string) string {
	return strings.ToUpper(id[:1]) + id[1:]
}

// Every operation of api/openapi.yaml is generated (strict interface method
// and route wrapper) and mounted: a request to its path reaches the
// generated binding of that operation, the SSE operation copyMessages
// included.
func TestSpecOperations_AllGeneratedAndMounted(t *testing.T) {
	t.Parallel()

	doc, err := loadSpec()
	require.NoError(t, err)
	strictType := reflect.TypeOf((*gen.StrictServerInterface)(nil)).Elem()
	wrapperType := reflect.TypeOf(&gen.ServerInterfaceWrapper{})
	h := recordingServer(t)

	var ids []string
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			ids = append(ids, id)
			name := pascal(id)
			_, ok := strictType.MethodByName(name)
			assert.True(t, ok, "%s: StrictServerInterface has no %s", id, name)
			_, ok = wrapperType.MethodByName(name)
			assert.True(t, ok, "%s: ServerInterfaceWrapper has no %s", id, name)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, strictRequest(method, path, id))
			assert.Equal(t, 299, rec.Code, "%s %s (%s): %s", method, path, id, rec.Body.String())
			assert.Equal(t, name, rec.Header().Get(strictOperationHeader), "%s %s", method, path)
		}
	}
	assert.Len(t, ids, specOperationCount)
	assert.Contains(t, ids, "copyMessages")
	// No generated method is left without a spec operation.
	assert.Equal(t, specOperationCount, strictType.NumMethod(), "StrictServerInterface methods")
}

// Every route the chi router registers is backed by a spec operation and
// served by its generated binding, and every spec operation has a route: no
// hand-written route shadows or extends the contract.
func TestRouter_EveryRouteIsASpecOperation(t *testing.T) {
	t.Parallel()

	doc, err := loadSpec()
	require.NoError(t, err)
	specOps := map[string]string{} // "METHOD path" -> operationId
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			specOps[method+" "+path] = op.OperationID
		}
	}

	h := recordingServer(t)
	router, ok := h.(chi.Routes)
	require.True(t, ok, "server.New must return a chi router")
	routed := map[string]bool{}
	err = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = normalizeChiRoute(route)
		// Dev-only stub of the approuter endpoint (devauth build tag).
		if strings.HasPrefix(route, "/user-api/") {
			return nil
		}
		key := method + " " + route
		routed[key] = true
		id, ok := specOps[key]
		if !assert.True(t, ok, "route %s is not an operation of api/openapi.yaml", key) {
			return nil
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, strictRequest(method, route, id))
		assert.Equal(t, 299, rec.Code, "%s: %s", key, rec.Body.String())
		assert.Equal(t, pascal(id), rec.Header().Get(strictOperationHeader), "%s is not served by the generated %s", key, id)
		return nil
	})
	require.NoError(t, err)
	for key, id := range specOps {
		assert.True(t, routed[key], "operation %s (%s) has no route", id, key)
	}
	for key := range routed {
		if strings.HasPrefix(strings.SplitN(key, " ", 2)[1], "/api/v1") {
			continue
		}
		// Outside /api/v1 only the probes are routes.
		assert.Contains(t, []string{"GET /healthz", "GET /readyz"}, key)
	}
}
