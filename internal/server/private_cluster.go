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

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/netguard"
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

// maxBrokersPerCluster caps the brokers of a private cluster definition (the
// X-Kafkito-Cluster header, a Test connection body, a dest_cluster_config).
// It is checked in code on every path; the OpenAPI document states the
// limit in the description only, so the request contract stays compatible.
const maxBrokersPerCluster = 50

// rejectInternalClusterNames answers a {cluster} path value in the internal
// namespace of private clusters (config.AdhocClusterPrefix) with the 404 of
// any unknown cluster, before RBAC or a handler sees the name. Such names
// are only valid as the result of resolvePrivateClusterParam, which derives
// them from __private__ and the X-Kafkito-Cluster header of the same
// request. It runs first in the cluster route group.
func rejectInternalClusterNames(errs errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A value that does not decode is left to the 400 further down.
			if cluster, err := pathParam(r, "cluster"); err == nil && config.IsAdhocClusterName(cluster) {
				errs.writeError(w, r, unknownClusterError(cluster, kafkapkg.ErrUnknownCluster))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type (
	privateCtxKey       struct{}
	hostValidatorCtxKey struct{}
)

// hostValidatorMiddleware gives each request one netguard.HostValidator that
// resolves with lookup (nil: the default resolver). Every cluster definition
// of the request (the X-Kafkito-Cluster header, a Test connection body, a
// dest_cluster_config) is checked with it, so each distinct host is resolved
// at most once per request.
func hostValidatorMiddleware(lookup netguard.LookupFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), hostValidatorCtxKey{}, netguard.NewHostValidator(lookup))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requestHostValidator returns the HostValidator of the request (see
// hostValidatorMiddleware), or a new one.
func requestHostValidator(ctx context.Context) *netguard.HostValidator {
	if v, ok := ctx.Value(hostValidatorCtxKey{}).(*netguard.HostValidator); ok {
		return v
	}
	return netguard.NewHostValidator(nil)
}

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
		cfg, err := decodePrivateClusterHeader(r.Context(), raw)
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

// decodePrivateClusterHeader decodes and validates the header value. Its
// errors are fixed texts that never repeat a value from the header.
func decodePrivateClusterHeader(ctx context.Context, raw string) (config.ClusterConfig, error) {
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
	if err := validatePrivateClusterConfig(ctx, cfg); err != nil {
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
func validatePrivateClusterConfig(ctx context.Context, cfg config.ClusterConfig) error {
	if len(cfg.Brokers) == 0 {
		return errors.New("at least one broker is required")
	}
	return validateClusterPolicy(ctx, cfg)
}

// validateClusterPolicy checks the rules of a cluster definition that the
// OpenAPI document does not express, or does not apply to the
// X-Kafkito-Cluster header because the header is not schema-validated: at
// most maxBrokersPerCluster brokers, non-blank broker addresses, the
// outbound-host (SSRF) policy for broker and Schema Registry hosts, and the
// credentials a SASL mechanism requires. It also rejects unknown auth
// types, which only the header can carry: it accepts auth.type
// case-insensitively and trimmed, while request bodies are held to the
// spec's lowercase enum by the request validator.
//
// The broker count is checked before any host is resolved; the host checks
// use the request's HostValidator (requestHostValidator).
//
// Its messages are fixed texts that name a broker by its 1-based position.
// They never repeat a submitted value (host, URL, auth type), a resolved
// address or a resolver error, because the caller returns them to the
// client.
func validateClusterPolicy(ctx context.Context, cfg config.ClusterConfig) error {
	return validateClusterPolicyWith(ctx, cfg, requestHostValidator(ctx))
}

// validateClusterPolicyWith is validateClusterPolicy with the host checks
// of v, which resolves each distinct host once.
func validateClusterPolicyWith(ctx context.Context, cfg config.ClusterConfig, v *netguard.HostValidator) error {
	if len(cfg.Brokers) > maxBrokersPerCluster {
		return fmt.Errorf("too many brokers (max %d)", maxBrokersPerCluster)
	}
	for i, b := range cfg.Brokers {
		b = strings.TrimSpace(b)
		if b == "" {
			return fmt.Errorf("broker %d: address must not be empty", i+1)
		}
		if err := v.Host(ctx, b); err != nil {
			return fmt.Errorf("broker %d: %w", i+1, err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Auth.Type)) {
	case "", "none":
	case "plain", "scram-sha-256", "scram-sha-512":
		if cfg.Auth.Username == "" || cfg.Auth.Password == "" {
			return errors.New("auth.username and auth.password are required for SASL")
		}
	default:
		return errors.New("auth.type not supported")
	}
	if u := strings.TrimSpace(cfg.SchemaRegistry.URL); u != "" {
		if err := v.URL(ctx, u); err != nil {
			return fmt.Errorf("schema_registry.url: %w", err)
		}
	}
	return nil
}

// errTextAdhocRegister is the client text for a failed UseAdhoc. The
// registry's own errors can name internal cluster names.
const errTextAdhocRegister = "private cluster settings could not be registered"

// resolvePrivateClusterParam is a Chi middleware that rewrites the
// "cluster" URL parameter from the private-cluster sentinel to the
// deterministic ad-hoc registry name. It runs AFTER rbacMiddleware so that
// RBAC observes the sentinel value and skips the policy check; all
// downstream handlers, in contrast, observe the real registry name and
// operate normally against the ad-hoc cluster. It is the only source of
// ad-hoc names in a request: rejectInternalClusterNames refuses them as a
// path value.
func resolvePrivateClusterParam(reg adhocClusters) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.RouteContext(r.Context())
			if rctx == nil {
				next.ServeHTTP(w, r)
				return
			}
			cluster, err := pathParam(r, "cluster")
			if err != nil {
				writeInvalidPathParam(w, "cluster")
				return
			}
			if cluster != config.PrivateClusterSentinel {
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
					"error": errTextAdhocRegister,
				})
				return
			}
			setPathParam(r, "cluster", effective)
			next.ServeHTTP(w, r)
		})
	}
}
