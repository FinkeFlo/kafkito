// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

const wantDefaultCSP = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

func assertSecurityHeaders(t *testing.T, h http.Header, wantCSP string, wantXFO string) {
	t.Helper()
	assert.Equal(t, wantCSP, h.Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))
	assert.Equal(t, "same-origin", h.Get("Cross-Origin-Opener-Policy"))
	assert.Equal(t, wantXFO, h.Get("X-Frame-Options"))
	assert.Empty(t, h.Get("Strict-Transport-Security"), "HSTS belongs to the TLS-terminating proxy")
}

// Every surface of the router carries the headers: API (success, 404, 405,
// private-cluster 400), health probes, the SPA shell and its fallback, and
// asset responses.
func TestSecurityHeaders_OnAllRoutes(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	h := New(Options{Version: "x", Logger: slog.Default(), Registry: reg, Config: config.Defaults()})

	cases := []struct {
		name, method, path string
		header             map[string]string
	}{
		{name: "api", method: http.MethodGet, path: "/api/v1/info"},
		{name: "openapi spec", method: http.MethodGet, path: "/api/v1/openapi.yaml"},
		{name: "api 404", method: http.MethodGet, path: "/api/v1/nope"},
		{name: "api 405", method: http.MethodDelete, path: "/api/v1/info"},
		{name: "private cluster 400", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header: map[string]string{PrivateClusterHeader: "!!!"}},
		{name: "healthz", method: http.MethodGet, path: "/healthz"},
		{name: "spa index", method: http.MethodGet, path: "/"},
		{name: "spa deep link", method: http.MethodGet, path: "/clusters/local/topics"},
		{name: "missing asset", method: http.MethodGet, path: "/assets/missing-abc123.js"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assertSecurityHeaders(t, rec.Header(), wantDefaultCSP, "DENY")
		})
	}
}

// Static files are served by http.FileServer; the middleware must wrap it
// like any other handler (the embedded dist/ only holds a placeholder
// index.html in unit tests, so use an in-memory FS here).
func TestSecurityHeaders_OnStaticAsset(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{"theme-init.js": {Data: []byte("(function(){})();")}}
	h := securityHeadersMiddleware("")(http.FileServer(http.FS(files)))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/theme-init.js", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "javascript")
	assertSecurityHeaders(t, rec.Header(), wantDefaultCSP, "DENY")
}

func TestSecurityHeaders_FrameAncestors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, configured, wantAncestors, wantXFO string
	}{
		{name: "empty means none", configured: "", wantAncestors: "'none'", wantXFO: "DENY"},
		{name: "explicit none", configured: "'none'", wantAncestors: "'none'", wantXFO: "DENY"},
		{name: "self", configured: "'self'", wantAncestors: "'self'", wantXFO: ""},
		{name: "allow list", configured: "'self' https://*.launchpad.example.com",
			wantAncestors: "'self' https://*.launchpad.example.com", wantXFO: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Defaults()
			cfg.Server.FrameAncestors = tc.configured
			h := New(Options{Version: "x", Logger: slog.Default(), Config: cfg})
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			wantCSP := wantDefaultCSP[:len(wantDefaultCSP)-len("'none'")] + tc.wantAncestors
			assertSecurityHeaders(t, rec.Header(), wantCSP, tc.wantXFO)
		})
	}
}

// A handler that sets its own headers and status must not drop the
// security headers.
func TestSecurityHeaders_KeptWhenHandlerSetsHeaders(t *testing.T) {
	t.Parallel()

	h := securityHeadersMiddleware("'none'")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusTeapot, rec.Code)
	assertSecurityHeaders(t, rec.Header(), wantDefaultCSP, "DENY")
}

// panicInHandler makes every generated handler panic, so the response comes
// from middleware.Recoverer.
func panicInHandler(gen.StrictHandlerFunc, string) gen.StrictHandlerFunc {
	return func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		panic("handler failed")
	}
}

// Every /api response is marked no-store, whoever writes it: a handler, the
// generated server, the request validator, the auth and private cluster
// middlewares, chi's 404 and 405, and the panic recovery. Health probes and
// the SPA shell keep their own caching.
func TestNoStore_OnAPIResponses(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	opts := Options{Version: "x", Logger: slog.New(slog.DiscardHandler), Registry: reg, Config: config.Defaults()}
	h := New(opts)
	withAuth := opts
	withAuth.Auth = rejectingValidator{}
	hAuth := New(withAuth)
	panicking := opts
	panicking.strictMiddlewares = []gen.StrictMiddlewareFunc{panicInHandler}
	hPanic := New(panicking)

	cases := []struct {
		name         string
		handler      http.Handler
		method, path string
		header       map[string]string
		body         string
		wantStatus   int
		want         string
	}{
		{name: "api", handler: h, method: http.MethodGet, path: "/api/v1/info", wantStatus: 200, want: "no-store"},
		{name: "openapi spec", handler: h, method: http.MethodGet, path: "/api/v1/openapi.yaml", wantStatus: 200, want: "no-store"},
		{name: "api 404", handler: h, method: http.MethodGet, path: "/api/v1/nope", wantStatus: 404, want: "no-store"},
		{name: "api root 404", handler: h, method: http.MethodGet, path: "/api", wantStatus: 404, want: "no-store"},
		{name: "api 405", handler: h, method: http.MethodDelete, path: "/api/v1/info", wantStatus: 405, want: "no-store"},
		{name: "invalid body 400", handler: h, method: http.MethodPost, path: "/api/v1/clusters/_test",
			header: map[string]string{"Content-Type": "application/json"}, body: `{"brokers":[]}`, wantStatus: 400, want: "no-store"},
		{name: "private cluster header 400", handler: h, method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header: map[string]string{PrivateClusterHeader: "!!!"}, wantStatus: 400, want: "no-store"},
		{name: "unknown cluster 404", handler: h, method: http.MethodGet, path: "/api/v1/clusters/nope/topics", wantStatus: 404, want: "no-store"},
		{name: "unauthorized 401", handler: hAuth, method: http.MethodGet, path: "/api/v1/info",
			header: map[string]string{"Authorization": "Bearer x"}, wantStatus: 401, want: "no-store"},
		{name: "recovered panic 500", handler: hPanic, method: http.MethodGet, path: "/api/v1/info", wantStatus: 500, want: "no-store"},
		{name: "healthz", handler: h, method: http.MethodGet, path: "/healthz", wantStatus: 200, want: ""},
		{name: "readyz", handler: h, method: http.MethodGet, path: "/readyz", wantStatus: 200, want: ""},
		{name: "spa index", handler: h, method: http.MethodGet, path: "/", wantStatus: 200, want: "no-cache"},
		{name: "spa deep link", handler: h, method: http.MethodGet, path: "/clusters/local/topics", wantStatus: 200, want: "no-cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			got := rec.Header().Values("Cache-Control")
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, []string{tc.want}, got, "exactly one Cache-Control header")
		})
	}
}
