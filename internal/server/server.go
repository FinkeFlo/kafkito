// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package server wires the kafkito HTTP router and top-level handlers.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/frontend"
	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
	"github.com/FinkeFlo/kafkito/pkg/proto/kafkito/v1/kafkitov1connect"
	"github.com/FinkeFlo/kafkito/pkg/rbac"
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
	// the request context for /api/v1/* and /rpc/* routes. nil disables the
	// auth middleware (used by tests).
	Auth auth.Validator
}

// New returns a ready-to-serve http.Handler.
func New(opts Options) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.CleanPath)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	policy := rbac.Compile(opts.Config.RBAC)

	r.Get("/healthz", handleHealth)
	r.Get("/readyz", handleReady(opts.Registry))

	r.Route("/api", func(api chi.Router) {
		api.Route("/v1", func(v1 chi.Router) {
			if opts.Auth != nil {
				v1.Use(auth.MiddlewareFor(opts.Auth))
			}
			v1.Get("/info", handleInfo(opts.Version))
			v1.Get("/me", handleMe(policy))
			v1.Get("/openapi.yaml", handleOpenAPISpec)
			v1.Get("/docs", handleSwaggerUI)
			if opts.Registry != nil {
				v1.Group(func(g chi.Router) {
					g.Use(privateClusterMiddleware)
					g.Use(rbacMiddleware(policy))
					g.Use(resolvePrivateClusterParam(opts.Registry))
					(&clusterAPI{reg: opts.Registry, policy: policy}).mount(g)
				})
			}
		})
		api.NotFound(apiNotFound)
		api.MethodNotAllowed(apiMethodNotAllowed)
	})

	// Connect-RPC surface, parallel to REST. Mounted under /rpc to keep
	// procedure paths (/rpc/kafkito.v1.InfoService/GetInfo) clearly separated.
	connectPath, connectHandler := kafkitov1connect.NewInfoServiceHandler(newInfoConnectHandler(opts.Version))
	if opts.Auth != nil {
		r.With(auth.MiddlewareFor(opts.Auth)).Mount("/rpc"+connectPath, http.StripPrefix("/rpc", connectHandler))
	} else {
		r.Mount("/rpc"+connectPath, http.StripPrefix("/rpc", connectHandler))
	}

	mountUserAPIStub(r)

	spa, err := frontend.Handler()
	if err != nil {
		if opts.Logger != nil {
			opts.Logger.Error("failed to load embedded frontend", "err", err)
		}
		r.NotFound(apiNotFound)
		return r
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			apiNotFound(w, req)
			return
		}
		spa.ServeHTTP(w, req)
	})

	return r
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady reports overall readiness. With a kafka Registry, all configured
// clusters are probed with a 1s timeout; if any is unreachable (or no clusters
// are configured), the endpoint returns 503. Without a Registry, it still
// returns 200 ("server up, no kafka configured").
func handleReady(reg *kafkapkg.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reg == nil || len(reg.Names()) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":   "ok",
				"clusters": []any{},
				"note":     "no kafka clusters configured",
			})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		infos := reg.Describe(ctx, 1*time.Second)
		allOK := true
		for _, c := range infos {
			if !c.Reachable {
				allOK = false
				break
			}
		}
		status := http.StatusOK
		payload := "ready"
		if !allOK {
			status = http.StatusServiceUnavailable
			payload = "degraded"
		}
		writeJSON(w, status, map[string]any{
			"status":   payload,
			"clusters": infos,
		})
	}
}

func handleInfo(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"name":    "kafkito",
			"version": version,
		})
	}
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
