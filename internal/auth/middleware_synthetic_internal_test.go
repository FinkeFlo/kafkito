// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syntheticFake satisfies both Validator and the package-private
// syntheticValidator interface, so MiddlewareFor's type assertion routes
// it through the synthetic branch.
type syntheticFake struct {
	principal *Principal
}

func (s syntheticFake) Validate(_ context.Context, _ string) (*Principal, error) {
	return s.principal, nil
}

func (s syntheticFake) SyntheticPrincipal() *Principal {
	return s.principal
}

// plainFake satisfies Validator but NOT syntheticValidator, so
// MiddlewareFor's type assertion falls through to the bearer-token
// branch (Middleware) which rejects requests missing the Authorization
// header before Validate is ever called.
type plainFake struct{}

func (plainFake) Validate(_ context.Context, _ string) (*Principal, error) {
	return nil, errors.New("plainFake.Validate must not be called when no Authorization header is present")
}

func TestMiddlewareFor_RoutesToSyntheticBranch_WhenValidatorImplementsSyntheticValidator(t *testing.T) {
	t.Parallel()

	want := &Principal{Subject: "synth-1"}
	mw := MiddlewareFor(syntheticFake{principal: want})

	var gotPrincipal *Principal
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal, _ = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "synthetic branch must not reject requests missing Authorization")
	require.NotNil(t, gotPrincipal)
	assert.Equal(t, "synth-1", gotPrincipal.Subject)
}

func TestMiddlewareFor_RoutesToBearerBranch_WhenValidatorIsPlain(t *testing.T) {
	t.Parallel()

	mw := MiddlewareFor(plainFake{})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("plain validator must route through bearer branch which rejects on missing Authorization header")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "bearer branch must 401 a request missing Authorization")
}

func TestMiddlewareWithSyntheticPrincipal_InjectsExactPointerIntoContext(t *testing.T) {
	t.Parallel()

	want := &Principal{Subject: "synth-1", Email: "x@y"}
	mw := MiddlewareWithSyntheticPrincipal(want)

	var got *Principal
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	h.ServeHTTP(rec, req)

	require.Same(t, want, got, "the exact *Principal must be injected, not a copy")
}

func TestMiddlewareWithSyntheticPrincipal_InvokesHandler_WithoutAuthorizationHeader(t *testing.T) {
	t.Parallel()

	called := 0
	mw := MiddlewareWithSyntheticPrincipal(&Principal{Subject: "synth-1"})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, 1, called, "handler must run exactly once with no Authorization required")
	assert.Equal(t, http.StatusOK, rec.Code)
}
