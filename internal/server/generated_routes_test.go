// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// migratedOp describes an operation served by the generated strict server,
// together with the route it had as a hand-written handler.
type migratedOp struct {
	id      string
	method  string
	pattern string // chi route pattern == spec path
	group   string // groupRoot, groupMeta or groupCluster
	// RBAC permission resolved from the route pattern ("" = no check).
	resource, action string
}

const (
	groupRoot    = "root"    // no auth
	groupMeta    = "meta"    // auth
	groupCluster = "cluster" // auth + private cluster + RBAC + private param
)

// migratedOps must list exactly the include-operation-ids of
// api/oapi-codegen.yaml (TestMigratedOps_MatchCodegenConfig).
var migratedOps = []migratedOp{
	{id: "getHealth", method: http.MethodGet, pattern: "/healthz", group: groupRoot},
	{id: "getReadiness", method: http.MethodGet, pattern: "/readyz", group: groupRoot},
	{id: "getInfo", method: http.MethodGet, pattern: "/api/v1/info", group: groupMeta},
	{id: "getMe", method: http.MethodGet, pattern: "/api/v1/me", group: groupMeta},
	{id: "getOpenApiSpec", method: http.MethodGet, pattern: "/api/v1/openapi.yaml", group: groupMeta},
	{id: "listClusters", method: http.MethodGet, pattern: "/api/v1/clusters", group: groupCluster, resource: "cluster:*", action: "view"},
	{id: "testCluster", method: http.MethodPost, pattern: "/api/v1/clusters/_test", group: groupCluster},
	{id: "getCapabilities", method: http.MethodGet, pattern: "/api/v1/clusters/{cluster}/capabilities", group: groupCluster, resource: "cluster:{cluster}", action: "view"},
	{id: "refreshCapabilities", method: http.MethodPost, pattern: "/api/v1/clusters/{cluster}/capabilities/refresh", group: groupCluster, resource: "cluster:{cluster}", action: "view"},
	{id: "listBrokers", method: http.MethodGet, pattern: "/api/v1/clusters/{cluster}/brokers", group: groupCluster},
}

func (op migratedOp) path(cluster string) string {
	return strings.ReplaceAll(op.pattern, "{cluster}", cluster)
}

func TestMigratedOps_MatchCodegenConfig(t *testing.T) {
	t.Parallel()

	f, err := os.Open("../../api/oapi-codegen.yaml")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var configured []string
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "include-operation-ids:":
			in = true
		case in && strings.HasPrefix(trimmed, "- "):
			configured = append(configured, strings.TrimPrefix(trimmed, "- "))
		case in && trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			in = false
		}
	}
	require.NoError(t, sc.Err())

	var listed []string
	for _, op := range migratedOps {
		listed = append(listed, op.id)
	}
	assert.ElementsMatch(t, configured, listed)

	doc, err := loadSpec()
	require.NoError(t, err)
	for _, op := range migratedOps {
		item := doc.Paths.Find(op.pattern)
		require.NotNil(t, item, op.pattern)
		o := item.GetOperation(op.method)
		require.NotNil(t, o, "%s %s", op.method, op.pattern)
		assert.Equal(t, op.id, o.OperationID)
	}
}

// Generated operations must keep their chi route patterns: RBAC derives the
// permission from them, and the request log reports them.
func TestMigratedOps_KeepRoutePatterns(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "test", Logger: slog.Default(), Registry: kafkapkg.NewRegistry(nil, slog.Default())})
	routes := map[string]bool{}
	require.NoError(t, chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+normalizeChiRoute(route)] = true
		return nil
	}))
	for _, op := range migratedOps {
		assert.True(t, routes[op.method+" "+op.pattern], "%s %s not registered", op.method, op.pattern)
	}
}

// rejectingValidator fails every token, like the auth middleware's
// validators do for an invalid bearer token.
type rejectingValidator struct{}

func (rejectingValidator) Validate(context.Context, string) (*auth.Principal, error) {
	return nil, errors.New("invalid token")
}

type acceptingValidator struct{}

func (acceptingValidator) Validate(context.Context, string) (*auth.Principal, error) {
	return &auth.Principal{Subject: "s-1", UserName: "alice"}, nil
}

// Each migrated operation runs behind the same middleware chain as before:
// the probes without auth, everything under /api/v1 behind it.
func TestMigratedOps_AuthMiddleware(t *testing.T) {
	t.Parallel()

	for _, v := range []struct {
		name  string
		auth  auth.Validator
		token string
	}{
		{"no token", rejectingValidator{}, ""},
		{"invalid token", rejectingValidator{}, "Bearer nope"},
	} {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			reg := kafkapkg.NewRegistry(nil, slog.Default())
			t.Cleanup(reg.Close)
			h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Auth: v.auth})
			for _, op := range migratedOps {
				req := httptest.NewRequest(op.method, op.path("local"), nil)
				if v.token != "" {
					req.Header.Set("Authorization", v.token)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if op.group == groupRoot {
					assert.Equal(t, http.StatusOK, rec.Code, "%s must not require auth: %s", op.id, rec.Body.String())
				} else {
					assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s must require auth: %s", op.id, rec.Body.String())
				}
			}
		})
	}

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()
		h := New(Options{Version: "test", Logger: slog.Default(), Auth: acceptingValidator{}})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer ok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"user":"alice"`)
	})
}

func denyAllRBAC() config.Config {
	cfg := config.Defaults()
	cfg.RBAC = config.RBACConfig{Enabled: true, Identity: config.IdentityConfig{Header: rbacTestHeader}}
	return cfg
}

// RBAC resolves the same resource and action from the generated routes as
// from the former hand-written ones, and runs after auth.
func TestMigratedOps_RBAC(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "rbac-c", Brokers: []string{"127.0.0.1:1"}}}, slog.Default())
	t.Cleanup(reg.Close)
	h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: denyAllRBAC()})

	for _, op := range migratedOps {
		if op.group != groupCluster {
			continue
		}
		t.Run(op.id, func(t *testing.T) {
			t.Parallel()
			var body *strings.Reader
			if op.id == "testCluster" {
				body = strings.NewReader(`{"brokers":["127.0.0.1:9092"]}`)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(op.method, op.path("rbac-c"), body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(rbacTestHeader, "mallory")
			ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
			defer cancel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req.WithContext(ctx))

			if op.resource == "" {
				assert.NotEqual(t, http.StatusForbidden, rec.Code, rec.Body.String())
				return
			}
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"error":"forbidden","resource":"`+strings.ReplaceAll(op.resource, "{cluster}", "rbac-c")+`","action":"`+op.action+`"}`, rec.Body.String())
		})
	}

	t.Run("auth runs before RBAC", func(t *testing.T) {
		t.Parallel()
		h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: denyAllRBAC(), Auth: rejectingValidator{}})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("malformed private header is rejected before RBAC", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/rbac-c/capabilities", nil)
		req.Header.Set(PrivateClusterHeader, "%%%")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.JSONEq(t, `{"error":"X-Kafkito-Cluster: invalid base64"}`, rec.Body.String())
	})

	t.Run("private clusters bypass RBAC", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/__private__/brokers", nil)
		req.Header.Set(PrivateClusterHeader, encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}}))
		ctx, cancel := context.WithTimeout(req.Context(), 300*time.Millisecond)
		defer cancel()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(ctx))
		assert.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	})
}

// recordingServer records the {cluster} value the generated wrapper bound.
type recordingServer struct {
	*apiServer
	got []string
}

func (s *recordingServer) GetCapabilities(_ context.Context, req gen.GetCapabilitiesRequestObject) (gen.GetCapabilitiesResponseObject, error) {
	s.got = append(s.got, req.Cluster)
	return gen.GetCapabilities200JSONResponse{Cluster: req.Cluster}, nil
}

func (s *recordingServer) RefreshCapabilities(_ context.Context, req gen.RefreshCapabilitiesRequestObject) (gen.RefreshCapabilitiesResponseObject, error) {
	s.got = append(s.got, req.Cluster)
	return gen.RefreshCapabilities200JSONResponse{Cluster: req.Cluster}, nil
}

func (s *recordingServer) ListBrokers(_ context.Context, req gen.ListBrokersRequestObject) (gen.ListBrokersResponseObject, error) {
	s.got = append(s.got, req.Cluster)
	return gen.ListBrokers200JSONResponse{Cluster: req.Cluster}, nil
}

// resolvePrivateClusterParam rewrites {cluster} before the generated wrapper
// binds it, so handlers see the ad-hoc registry name, not the sentinel.
func TestMigratedOps_PrivateClusterParam(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "static", Brokers: []string{"127.0.0.1:1"}}}, slog.Default())
	t.Cleanup(reg.Close)
	rec := &recordingServer{apiServer: &apiServer{reg: reg, policy: rbac.Compile(config.RBACConfig{})}}
	routes, err := newGeneratedRoutes(rec, errorWriter{log: slog.Default()})
	require.NoError(t, err)
	// Same group middleware as server.New.
	r := chi.NewRouter()
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Group(func(g chi.Router) {
			g.Use(privateClusterMiddleware)
			g.Use(rbacMiddleware(rec.policy))
			g.Use(resolvePrivateClusterParam(reg))
			routes.mountClusters(g)
		})
	})

	cfg := config.ClusterConfig{Brokers: []string{unreachableBroker}}
	adhoc, err := reg.UseAdhoc(cfg)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(adhoc, kafkapkg.AdhocPrefix))
	header := encodeHeader(t, cfg)

	for _, tc := range []struct{ method, path, header, want string }{
		{http.MethodGet, "/api/v1/clusters/__private__/capabilities", header, adhoc},
		{http.MethodPost, "/api/v1/clusters/__private__/capabilities/refresh", header, adhoc},
		{http.MethodGet, "/api/v1/clusters/__private__/brokers", header, adhoc},
		{http.MethodGet, "/api/v1/clusters/static/brokers", "", "static"},
		{http.MethodGet, "/api/v1/clusters/static/brokers", header, "static"},
	} {
		rec.got = nil
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.header != "" {
			req.Header.Set(PrivateClusterHeader, tc.header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "%s %s: %s", tc.method, tc.path, w.Body.String())
		assert.Equal(t, []string{tc.want}, rec.got, "%s %s", tc.method, tc.path)
		assert.Contains(t, w.Body.String(), `"cluster":"`+tc.want+`"`)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/__private__/brokers", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"error":"private cluster requires X-Kafkito-Cluster header"}`, w.Body.String())
}

// contractRouter matches requests to operations of the embedded spec.
func contractRouter(t *testing.T) routers.Router {
	t.Helper()
	doc, err := loadSpec()
	require.NoError(t, err)
	cp := *doc
	cp.Servers = nil
	router, err := gorillamux.NewRouter(&cp)
	require.NoError(t, err)
	return router
}

// assertResponseMatchesSpec validates a real response against the status,
// headers and schema the spec documents for req's operation, and returns the
// operationId.
func assertResponseMatchesSpec(t *testing.T, router routers.Router, req *http.Request, rec *httptest.ResponseRecorder) string {
	t.Helper()
	req = withCleanURLPath(req)
	route, params, err := router.FindRoute(req)
	require.NoError(t, err, "%s %s is not in the spec", req.Method, req.URL.Path)
	opts := &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		IncludeResponseStatus: true,
		// The raw YAML document does not decode to the documented string.
		ExcludeResponseBody: route.Operation.OperationID == "getOpenApiSpec",
	}
	in := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request: req, PathParams: params, Route: route, Options: opts,
		},
		Status:  rec.Code,
		Header:  rec.Header(),
		Options: opts,
	}
	in.SetBodyBytes(rec.Body.Bytes())
	assert.NoError(t, openapi3filter.ValidateResponse(context.Background(), in),
		"%s %s -> %d %s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
	return route.Operation.OperationID
}

// requestCase is a real request against server.New whose response is
// checked for its status, body and conformance to the spec.
type requestCase struct {
	name        string
	method      string
	path        string
	header      map[string]string
	body        string
	contentType string
	timeout     time.Duration
	wantStatus  int
	wantBody    string // substring
	// notAnOperation marks responses of chi's fallback handlers, which the
	// spec does not describe per operation.
	notAnOperation bool
}

// TestMigratedOps_Requests sends one or more valid and invalid requests per
// migrated operation, including the boundaries of the former hand-written
// validation, and validates every response against the spec.
func TestMigratedOps_Requests(t *testing.T) {
	t.Parallel()

	broker := startFakeBroker(t)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{
		{Name: "up", Brokers: []string{broker.addr()}},
		{Name: "down", Brokers: []string{"127.0.0.1:1"}},
	}, slog.Default())
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
	h := New(Options{Version: "v-test", Logger: slog.Default(), Registry: reg, Config: cfg})

	upOnly := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "up-only", Brokers: []string{broker.addr()}}}, slog.Default())
	t.Cleanup(upOnly.Close)
	hUp := New(Options{Version: "v-test", Logger: slog.Default(), Registry: upOnly, Config: cfg})
	hNoKafka := New(Options{Version: "v-test", Logger: slog.Default()})

	unreachableHeader := encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
	jsonCT := "application/json"

	cases := []struct {
		handler http.Handler
		requestCase
	}{
		// getHealth
		{h, requestCase{name: "health", method: http.MethodGet, path: "/healthz", wantStatus: 200, wantBody: `{"status":"ok"}`}},
		{h, requestCase{name: "health ignores a body", method: http.MethodGet, path: "/healthz", body: "junk", wantStatus: 200}},
		{h, requestCase{name: "health wrong method", method: http.MethodPost, path: "/healthz", wantStatus: 405, notAnOperation: true}},
		// getReadiness
		{hNoKafka, requestCase{name: "ready without kafka", method: http.MethodGet, path: "/readyz", wantStatus: 200, wantBody: `"note":"no kafka clusters configured"`}},
		{hUp, requestCase{name: "ready", method: http.MethodGet, path: "/readyz", wantStatus: 200, wantBody: `"status":"ready"`}},
		{h, requestCase{name: "ready degraded", method: http.MethodGet, path: "/readyz", wantStatus: 503, wantBody: `"status":"degraded"`}},
		{h, requestCase{name: "ready wrong method", method: http.MethodDelete, path: "/readyz", wantStatus: 405, notAnOperation: true}},
		// getInfo
		{h, requestCase{name: "info", method: http.MethodGet, path: "/api/v1/info", wantStatus: 200, wantBody: `{"name":"kafkito","version":"v-test"}`}},
		{h, requestCase{name: "info unclean path", method: http.MethodGet, path: "/api//v1/info", wantStatus: 200}},
		{h, requestCase{name: "info wrong method", method: http.MethodPost, path: "/api/v1/info", wantStatus: 405, wantBody: `{"error":"method not allowed"}`, notAnOperation: true}},
		// getMe
		{h, requestCase{name: "me anonymous", method: http.MethodGet, path: "/api/v1/me", wantStatus: 200, wantBody: `"scopes":null`}},
		{h, requestCase{name: "me wrong method", method: http.MethodPut, path: "/api/v1/me", wantStatus: 405, notAnOperation: true}},
		// getOpenApiSpec
		{h, requestCase{name: "openapi", method: http.MethodGet, path: "/api/v1/openapi.yaml", wantStatus: 200, wantBody: "openapi: 3.1.0"}},
		{h, requestCase{name: "openapi wrong method", method: http.MethodPost, path: "/api/v1/openapi.yaml", wantStatus: 405, notAnOperation: true}},
		// listClusters
		{h, requestCase{name: "clusters", method: http.MethodGet, path: "/api/v1/clusters", wantStatus: 200, wantBody: `"name":"up","reachable":true`}},
		{h, requestCase{name: "clusters wrong method", method: http.MethodDelete, path: "/api/v1/clusters", wantStatus: 405, notAnOperation: true}},
		// testCluster
		{h, requestCase{name: "test via header", method: http.MethodPost, path: "/api/v1/clusters/_test", header: map[string]string{PrivateClusterHeader: unreachableHeader}, wantStatus: 200, wantBody: `"reachable":false`}},
		{h, requestCase{name: "test via body", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":["` + unreachableBroker + `"],"auth":{"type":" PLAIN ","username":"u","password":"p"}}`, wantStatus: 200, wantBody: `"auth_type":"plain"`}},
		{h, requestCase{name: "test body with charset", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: "application/json; charset=utf-8", body: `{"brokers":["` + unreachableBroker + `"],"auth":{"type":"scram-sha-512","username":"u","password":"p"}}`, wantStatus: 200}},
		{h, requestCase{name: "test without config", method: http.MethodPost, path: "/api/v1/clusters/_test", wantStatus: 400, wantBody: `{"error":"cluster config required in body or X-Kafkito-Cluster header"}`}},
		{h, requestCase{name: "test no brokers", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":[]}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body \"/brokers\": must have at least 1 items"}`}},
		{h, requestCase{name: "test brokers missing", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{}`, wantStatus: 400, wantBody: `"error":"request body \"/brokers\": is required"`}},
		{h, requestCase{name: "test blank broker", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":["h:1"," "]}`, wantStatus: 400, wantBody: `"error":"request body \"/brokers/1\": must match pattern '\\S'"`}},
		{h, requestCase{name: "test unsupported auth type", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":["h:1"],"auth":{"type":"kerberos"}}`, wantStatus: 400, wantBody: `"error":"request body \"/auth/type\": must match pattern`}},
		{h, requestCase{name: "test plain without password", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":["` + unreachableBroker + `"],"auth":{"type":"plain","username":"u"}}`, wantStatus: 400, wantBody: `{"error":"auth \"plain\" requires username and password"}`}},
		{h, requestCase{name: "test SSRF-blocked broker", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":["127.0.0.1:9092"]}`, wantStatus: 400, wantBody: `"error":"broker \"127.0.0.1:9092\": `}},
		{h, requestCase{name: "test wrong type", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":["h:1"],"tls":{"enabled":"yes"}}`, wantStatus: 400, wantBody: `"error":"request body \"/tls/enabled\": must be of type boolean"`}},
		{h, requestCase{name: "test malformed JSON", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: jsonCT, body: `{"brokers":`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: malformed"}`}},
		{h, requestCase{name: "test wrong content type", method: http.MethodPost, path: "/api/v1/clusters/_test", contentType: "application/x-www-form-urlencoded", body: `{"brokers":["h:1"]}`, wantStatus: 400, wantBody: `{"code":"invalid_request","error":"request body: unsupported Content-Type"}`}},
		{h, requestCase{name: "test wrong method", method: http.MethodGet, path: "/api/v1/clusters/_test", wantStatus: 405, notAnOperation: true}},
		// getCapabilities
		{h, requestCase{name: "capabilities", method: http.MethodGet, path: "/api/v1/clusters/up/capabilities", wantStatus: 200, wantBody: `"cluster":"up"`}},
		{h, requestCase{name: "capabilities unknown cluster", method: http.MethodGet, path: "/api/v1/clusters/nope/capabilities", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`}},
		{h, requestCase{name: "capabilities private without header", method: http.MethodGet, path: "/api/v1/clusters/__private__/capabilities", wantStatus: 400}},
		{h, requestCase{name: "capabilities malformed header", method: http.MethodGet, path: "/api/v1/clusters/up/capabilities", header: map[string]string{PrivateClusterHeader: "not base64!"}, wantStatus: 400, wantBody: `{"error":"X-Kafkito-Cluster: invalid base64"}`}},
		// refreshCapabilities
		{h, requestCase{name: "refresh", method: http.MethodPost, path: "/api/v1/clusters/up/capabilities/refresh", wantStatus: 200, wantBody: `"cluster":"up"`}},
		{h, requestCase{name: "refresh ignores a body", method: http.MethodPost, path: "/api/v1/clusters/up/capabilities/refresh", contentType: "text/plain", body: "junk", wantStatus: 200}},
		{h, requestCase{name: "refresh unknown cluster", method: http.MethodPost, path: "/api/v1/clusters/nope/capabilities/refresh", wantStatus: 404}},
		{h, requestCase{name: "refresh wrong method", method: http.MethodGet, path: "/api/v1/clusters/up/capabilities/refresh", wantStatus: 405, notAnOperation: true}},
		// listBrokers
		{h, requestCase{name: "brokers", method: http.MethodGet, path: "/api/v1/clusters/up/brokers", wantStatus: 200, wantBody: `"is_controller":true`}},
		{h, requestCase{name: "brokers unknown cluster", method: http.MethodGet, path: "/api/v1/clusters/nope/brokers", wantStatus: 404, wantBody: `{"error":"unknown cluster: nope"}`}},
		{h, requestCase{name: "brokers unreachable", method: http.MethodGet, path: "/api/v1/clusters/down/brokers", timeout: time.Second, wantStatus: 502, wantBody: `{"code":"kafka_upstream","error":"upstream kafka error"}`}},
		{h, requestCase{name: "brokers private without header", method: http.MethodGet, path: "/api/v1/clusters/__private__/brokers", wantStatus: 400}},
	}

	router := contractRouter(t)
	seen := map[string]map[string]bool{} // operationId -> "2xx"/"4xx"
	for _, tc := range cases {
		req, rec := tc.do(t, tc.handler)
		assert.Equal(t, tc.wantStatus, rec.Code, "%s: %s", tc.name, rec.Body.String())
		if tc.wantBody != "" {
			assert.Contains(t, rec.Body.String(), tc.wantBody, tc.name)
		}
		if tc.notAnOperation {
			continue
		}
		id := assertResponseMatchesSpec(t, router, req, rec)
		if seen[id] == nil {
			seen[id] = map[string]bool{}
		}
		seen[id][statusClass(rec.Code)] = true
	}
	for _, op := range migratedOps {
		assert.True(t, seen[op.id]["2xx"], "%s: no successful response validated", op.id)
	}
}

func (tc requestCase) do(t *testing.T, h http.Handler) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	var body *strings.Reader
	if tc.body != "" {
		body = strings.NewReader(tc.body)
	}
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(tc.method, tc.path, body)
	} else {
		req = httptest.NewRequest(tc.method, tc.path, nil)
	}
	if tc.contentType != "" {
		req.Header.Set("Content-Type", tc.contentType)
	}
	for k, v := range tc.header {
		req.Header.Set(k, v)
	}
	if tc.timeout > 0 {
		ctx, cancel := context.WithTimeout(req.Context(), tc.timeout)
		t.Cleanup(cancel)
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return req, rec
}

func statusClass(code int) string {
	return strconv.Itoa(code/100) + "xx"
}

// The testCluster body limit (8 KiB, 400) applies before the validator
// reads the body, so an oversized body is never buffered.
func TestTestCluster_BodyLimit(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.Server.TestConnectionTimeout = 200 * time.Millisecond
	h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: cfg})

	padded := func(n int) string {
		base := `{"brokers":["` + unreachableBroker + `"]}`
		return base[:len(base)-1] + strings.Repeat(" ", n-len(base)) + "}"
	}
	for _, tc := range []struct {
		size int
		want int
	}{
		{maxPrivateClusterHeaderBytes, http.StatusOK},
		{maxPrivateClusterHeaderBytes + 1, http.StatusBadRequest},
	} {
		body := padded(tc.size)
		require.Len(t, body, tc.size)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, tc.want, rec.Code, "size %d: %s", tc.size, rec.Body.String())
		if tc.want == http.StatusBadRequest {
			assert.JSONEq(t, `{"error":"invalid body: http: request body too large"}`, rec.Body.String())
		}
	}

	t.Run("endless body is not read past the limit", func(t *testing.T) {
		t.Parallel()
		src := &countingReader{}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", src)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.LessOrEqual(t, src.n, int64(2*maxPrivateClusterHeaderBytes), "read %d bytes", src.n)
	})
}

// countingReader yields an endless JSON-ish stream and counts what is read.
type countingReader struct{ n int64 }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	c.n += int64(len(p))
	return len(p), nil
}

// Validation errors name the field and the rule, never the submitted value,
// and neither the password of a private cluster nor the raw
// X-Kafkito-Cluster header reaches the response or any log line.
func TestMigratedOps_ValidationErrorsNeverLeakCredentials(t *testing.T) {
	t.Parallel()

	header := encodeHeader(t, config.ClusterConfig{
		Brokers: []string{unreachableBroker},
		Auth:    config.AuthConfig{Type: "plain", Username: "leak-user", Password: leakPassword},
	})
	bodies := []string{
		`{"brokers":"` + leakPassword + `"}`,
		`{"brokers":["` + unreachableBroker + `"],"auth":{"type":"` + leakPassword + `","password":"` + leakPassword + `"}}`,
		`{"brokers":["` + unreachableBroker + `"],"is_prod":"` + leakPassword + `"}`,
		`{"brokers":["` + unreachableBroker + `"],"data_masking":"` + leakPassword + `"}`,
		`{"brokers":["` + unreachableBroker + `"],"auth":{"password":` + `{"` + leakPassword + `":1}}}`,
		`{"brokers":["` + leakPassword + `"`,
	}
	for i, body := range bodies {
		logs := &syncBuffer{}
		logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		reg := kafkapkg.NewRegistry(nil, logger)
		h := New(Options{Version: "x", Logger: logger, Registry: reg, Config: config.Defaults()})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(PrivateClusterHeader, header)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		reg.Close()

		require.Equal(t, http.StatusBadRequest, rec.Code, "body %d: %s", i, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"code":"invalid_request"`, "body %d", i)
		for _, secret := range []string{leakPassword, header} {
			assert.NotContains(t, rec.Body.String(), secret, "body %d: response leaks", i)
			assert.NotContains(t, logs.String(), secret, "body %d: logs leak", i)
		}
	}
}

func TestOpenAPISpec_ServedWithHeaders(t *testing.T) {
	t.Parallel()

	h := New(Options{Version: "test", Logger: slog.Default()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/yaml; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte("openapi: 3.1.0")))
}
