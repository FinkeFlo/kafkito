// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package server wires the kafkito HTTP router and top-level handlers.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/frontend"
	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Options configures the HTTP server.
type Options struct {
	Version  string
	Logger   *slog.Logger
	Registry *kafkapkg.Registry // may be nil (no kafka configured)
	Config   config.Config
	// Auth validates incoming bearer tokens and injects auth.Principal into
	// the request context for /api/v1/* routes. nil disables the
	// auth middleware (used by tests).
	Auth auth.Validator

	// copyRegistry replaces Registry for the copy job in tests.
	copyRegistry copyRegistry
}

// New returns a ready-to-serve http.Handler.
func New(opts Options) http.Handler {
	r := chi.NewRouter()

	baseLog := opts.Logger
	if baseLog == nil {
		baseLog = slog.Default()
	}
	// Handler loggers add request_id to every *Context log call.
	handlerLog := withRequestIDLogging(opts.Logger)

	r.Use(securityHeadersMiddleware(opts.Config.Server.FrameAncestors))
	r.Use(middleware.CleanPath)
	// Outside Recoverer so recovered panics are logged with status 500.
	r.Use(requestLogMiddleware(baseLog))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	policy := rbac.Compile(opts.Config.RBAC)

	copyReg := opts.copyRegistry
	if copyReg == nil && opts.Registry != nil {
		copyReg = opts.Registry
	}

	generated, err := newGeneratedRoutes(&apiServer{
		version:         opts.Version,
		policy:          policy,
		reg:             opts.Registry,
		copyReg:         copyReg,
		log:             handlerLog,
		testConnTimeout: opts.Config.Server.TestConnectionTimeout,
	}, errorWriter{log: handlerLog})
	if err != nil {
		// The document is embedded and covered by tests; failing here is a
		// build defect, not a runtime condition.
		panic(err)
	}

	generated.mountRoot(r)

	r.Route("/api", func(api chi.Router) {
		api.Route("/v1", func(v1 chi.Router) {
			if opts.Auth != nil {
				v1.Use(auth.MiddlewareFor(opts.Auth), capturePrincipal)
			}
			generated.mountMeta(v1)
			if opts.Registry != nil {
				v1.Group(func(g chi.Router) {
					g.Use(privateClusterMiddleware)
					g.Use(rbacMiddleware(policy))
					g.Use(resolvePrivateClusterParam(opts.Registry))
					generated.mountClusters(g)
				})
			}
		})
		api.NotFound(apiNotFound)
		api.MethodNotAllowed(apiMethodNotAllowed)
	})

	mountUserAPIStub(r)

	spa, err := frontend.Handler()
	if err != nil {
		if opts.Logger != nil {
			opts.Logger.Error("failed to load embedded frontend", "err", err)
		}
		r.NotFound(apiNotFound)
		return r
	}
	// SPA-fallback handler.
	//
	// Browser deep-link reload (e.g. ⌘R on /topics/foo/messages) lands here as
	// a fresh GET. We must serve index.html so TanStack Router can take over
	// on the client. But we MUST NOT:
	//   - 200-fall through for backend route prefixes (/api, /rpc, /user-api,
	//     /healthz, /readyz) when those didn't match — programmatic clients
	//     deserve a JSON 404, not the SPA shell;
	//   - serve HTML for non-GET/HEAD methods — POST/PUT/etc on an unknown
	//     path must surface as 405/404 cleanly;
	//   - serve HTML for missing /assets/* — browsers strict-MIME-check
	//     hashed JS/CSS bundles and would refuse the response.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isBackendPrefix(req.URL.Path) {
			apiNotFound(w, req)
			return
		}
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			apiNotFound(w, req)
			return
		}
		spa.ServeHTTP(w, req)
	})

	return r
}

// isBackendPrefix reports whether a request path belongs to a known backend
// surface and should therefore never fall through to the SPA shell.
func isBackendPrefix(p string) bool {
	switch {
	case strings.HasPrefix(p, "/api/"), p == "/api":
		return true
	// /rpc hosted the removed Connect-RPC surface (ADR-0005). Kept reserved so
	// stale clients get a JSON 404 instead of the SPA shell.
	case strings.HasPrefix(p, "/rpc/"), p == "/rpc":
		return true
	case strings.HasPrefix(p, "/user-api/"), p == "/user-api":
		return true
	case p == "/healthz", p == "/readyz":
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func apiMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}
