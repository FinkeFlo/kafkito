// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/FinkeFlo/kafkito/api"
	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// apiServer implements the generated strict server interface. Each handler
// gets typed inputs (already validated against the OpenAPI document) and
// returns a typed response or an error for writeError.
type apiServer struct {
	version         string
	policy          *rbac.Policy
	reg             *kafkapkg.Registry // nil without kafka configuration
	copyReg         copyRegistry       // reg, or a test fake
	log             *slog.Logger
	testConnTimeout time.Duration
}

var _ gen.StrictServerInterface = (*apiServer)(nil)

type httpRequestKey struct{}

// withHTTPRequest is a strict middleware that exposes the *http.Request to
// handlers that need more than the typed request object (e.g. the RBAC
// identity header in getMe).
func withHTTPRequest(f gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return f(context.WithValue(ctx, httpRequestKey{}, r), w, r, req)
	}
}

func httpRequestFromContext(ctx context.Context) *http.Request {
	r, _ := ctx.Value(httpRequestKey{}).(*http.Request)
	return r
}

// GetHealth is the liveness probe.
func (s *apiServer) GetHealth(context.Context, gen.GetHealthRequestObject) (gen.GetHealthResponseObject, error) {
	return gen.GetHealth200JSONResponse{Status: gen.HealthResponseStatusOk}, nil
}

// GetReadiness reports overall readiness. With a kafka Registry, all
// configured clusters are probed with a 1s timeout; if any is unreachable
// the endpoint returns 503. Without a Registry or clusters it still returns
// 200 ("server up, no kafka configured").
func (s *apiServer) GetReadiness(ctx context.Context, _ gen.GetReadinessRequestObject) (gen.GetReadinessResponseObject, error) {
	if s.reg == nil || len(s.reg.Names()) == 0 {
		note := "no kafka clusters configured"
		return gen.GetReadiness200JSONResponse{
			Status:   gen.ReadinessResponseStatusOk,
			Clusters: []kafkapkg.ClusterInfo{},
			Note:     &note,
		}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	infos := s.reg.Describe(ctx, 1*time.Second)
	for _, c := range infos {
		if !c.Reachable {
			return gen.GetReadiness503JSONResponse{Status: gen.ReadinessResponseStatusDegraded, Clusters: infos}, nil
		}
	}
	return gen.GetReadiness200JSONResponse{Status: gen.ReadinessResponseStatusReady, Clusters: infos}, nil
}

// GetInfo returns build info.
func (s *apiServer) GetInfo(context.Context, gen.GetInfoRequestObject) (gen.GetInfoResponseObject, error) {
	return gen.GetInfo200JSONResponse{Name: "kafkito", Version: s.version}, nil
}

// GetMe returns the current principal, roles and materialized permissions.
func (s *apiServer) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	// Prefer the JWT-derived principal; fall back to legacy header trust for compatibility.
	var (
		email  string
		scopes []string
		tenant string
		hasJWT bool
	)
	if p, ok := auth.PrincipalFromContext(ctx); ok {
		hasJWT = true
		email = p.Email
		scopes = p.Scopes
		tenant = p.Tenant
	}
	// rbacSubject is the single identity resolver: principal first, header fallback.
	user := rbacSubject(httpRequestFromContext(ctx), s.policy)
	return gen.GetMe200JSONResponse{
		User:        user,
		Email:       email,
		Tenant:      tenant,
		Scopes:      nullableStrings(scopes),
		Roles:       nullableStrings(s.policy.ResolveRoles(user)),
		Permissions: s.policy.MaterializePermissions(user),
		Anonymous:   user == "",
		Jwt:         hasJWT,
		RbacEnabled: s.policy.Enabled(),
	}, nil
}

// nullableStrings maps a nil slice to JSON null, as the spec's
// `type: [array, "null"]` fields expect.
func nullableStrings(s []string) *[]string {
	if s == nil {
		return nil
	}
	return &s
}

// openAPISpecResponse serves the raw document with an explicit charset and
// no caching; the generated response type only sets `application/yaml`.
type openAPISpecResponse struct{}

func (openAPISpecResponse) VisitGetOpenApiSpecResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err := bytes.NewReader(api.Spec).WriteTo(w)
	return err
}

// GetOpenApiSpec serves this OpenAPI 3.1 document.
func (s *apiServer) GetOpenApiSpec(context.Context, gen.GetOpenApiSpecRequestObject) (gen.GetOpenApiSpecResponseObject, error) {
	return openAPISpecResponse{}, nil
}

// testConnectionTimeout returns the budget for the user-driven Test
// connection probe (server.test_connection_timeout, env
// KAFKITO_TEST_CONNECTION_TIMEOUT). Non-positive values fall back to
// config.DefaultTestConnectionTimeout.
func (s *apiServer) testConnectionTimeout() time.Duration {
	if s.testConnTimeout <= 0 {
		return config.DefaultTestConnectionTimeout
	}
	return s.testConnTimeout
}
