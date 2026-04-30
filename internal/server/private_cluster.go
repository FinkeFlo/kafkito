// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
	"github.com/go-chi/chi/v5"
)

// PrivateClusterHeader carries a base64-encoded JSON ClusterConfig for ad-hoc
// ("private") clusters that the user maintains client-side (browser
// localStorage). The header is only honoured when the URL path uses
// config.PrivateClusterSentinel as the cluster segment.
const PrivateClusterHeader = "X-Kafkito-Cluster"

// maxPrivateClusterHeaderBytes caps the size of the decoded header payload to
// bound memory and quickly reject obvious abuse.
const maxPrivateClusterHeaderBytes = 8 * 1024

type privateCtxKey struct{}

// privateClusterMiddleware inspects the PrivateClusterHeader on every request.
// When present it decodes and validates a ClusterConfig and stashes it in the
// request context. Malformed headers are rejected with 400 to fail fast; an
// absent header is a no-op.
func privateClusterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(PrivateClusterHeader)
		if raw == "" {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := decodePrivateClusterHeader(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": PrivateClusterHeader + ": " + err.Error(),
			})
			return
		}
		ctx := context.WithValue(r.Context(), privateCtxKey{}, cfg)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func decodePrivateClusterHeader(raw string) (config.ClusterConfig, error) {
	if len(raw) > maxPrivateClusterHeaderBytes*2 {
		return config.ClusterConfig{}, fmt.Errorf("header too large")
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return config.ClusterConfig{}, fmt.Errorf("invalid base64")
	}
	if len(payload) > maxPrivateClusterHeaderBytes {
		return config.ClusterConfig{}, fmt.Errorf("payload too large")
	}
	var cfg config.ClusterConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return config.ClusterConfig{}, fmt.Errorf("invalid JSON")
	}
	if err := validatePrivateClusterConfig(cfg); err != nil {
		return config.ClusterConfig{}, err
	}
	return cfg, nil
}

func privateClusterFromContext(ctx context.Context) (config.ClusterConfig, bool) {
	v, ok := ctx.Value(privateCtxKey{}).(config.ClusterConfig)
	return v, ok
}

// validatePrivateClusterConfig enforces the minimum fields required to
// connect. Matches the rules in config.Validate for static clusters minus
// the name (caller-facing name doesn't matter for ad-hoc).
func validatePrivateClusterConfig(cfg config.ClusterConfig) error {
	if len(cfg.Brokers) == 0 {
		return errors.New("at least one broker is required")
	}
	for _, b := range cfg.Brokers {
		if strings.TrimSpace(b) == "" {
			return errors.New("broker address must not be empty")
		}
	}
	t := strings.ToLower(strings.TrimSpace(cfg.Auth.Type))
	switch t {
	case "", "none":
	case "plain", "scram-sha-256", "scram-sha-512":
		if cfg.Auth.Username == "" || cfg.Auth.Password == "" {
			return fmt.Errorf("auth %q requires username and password", t)
		}
	default:
		return fmt.Errorf("auth.type %q not supported", cfg.Auth.Type)
	}
	return nil
}

// resolvePrivateClusterParam is a Chi middleware that rewrites the
// "cluster" URL parameter from the private-cluster sentinel to the
// deterministic ad-hoc registry name. It runs AFTER rbacMiddleware so that
// RBAC observes the sentinel value and can bypass policy enforcement; all
// downstream handlers, in contrast, observe the real registry name and
// operate normally against the ad-hoc cluster.
func resolvePrivateClusterParam(reg *kafkapkg.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.RouteContext(r.Context())
			if rctx == nil {
				next.ServeHTTP(w, r)
				return
			}
			if chi.URLParam(r, "cluster") != config.PrivateClusterSentinel {
				next.ServeHTTP(w, r)
				return
			}
			cfg, ok := privateClusterFromContext(r.Context())
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "private cluster requires " + PrivateClusterHeader + " header",
				})
				return
			}
			effective, err := reg.UseAdhoc(cfg)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": err.Error(),
				})
				return
			}
			for i, k := range rctx.URLParams.Keys {
				if k == "cluster" {
					rctx.URLParams.Values[i] = effective
					break
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
