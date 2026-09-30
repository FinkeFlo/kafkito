// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// newMiddlewareValidator builds a generic OIDC validator backed by a MockOIDC
// fixture. The returned audience is the clientID expected by Issue().
func newMiddlewareValidator(t *testing.T) (mock *auth.MockOIDC, v auth.Validator, audience string) {
	t.Helper()

	m, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(m.Close)

	const aud = "kafkito-test"
	val, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    m.Server.URL,
		Audience:     aud,
		JWKSEndpoint: m.JKU(),
	})
	require.NoError(t, err, "NewOIDCValidator")

	return m, val, aud
}

func TestMiddleware_AllowsAuthorizedRequest_InvokesHandler(t *testing.T) {
	t.Parallel()

	mock, v, aud := newMiddlewareValidator(t)
	tok, err := mock.Issue("u-1", aud, mock.Server.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	called := false
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "handler must be invoked when token is valid")
}

func TestMiddleware_PutsPrincipalIntoRequestContext(t *testing.T) {
	t.Parallel()

	mock, v, aud := newMiddlewareValidator(t)
	tok, err := mock.Issue("u-1", aud, mock.Server.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	var (
		gotPrincipal *auth.Principal
		gotOK        bool
	)
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal, gotOK = auth.PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	h.ServeHTTP(rec, req)

	require.True(t, gotOK, "principal must be present in request context")
	require.NotNil(t, gotPrincipal)
	assert.Equal(t, "u-1", gotPrincipal.Subject)
}

func TestMiddleware_RejectsRequest_WhenAuthorizationHeaderMissing(t *testing.T) {
	t.Parallel()

	_, v, _ := newMiddlewareValidator(t)
	called := false
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	h.ServeHTTP(rec, req)

	assert.False(t, called, "handler must not run when Authorization header is missing")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMiddleware_RejectsRequest_WhenBearerTokenMalformed(t *testing.T) {
	t.Parallel()

	_, v, _ := newMiddlewareValidator(t)
	called := false
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")

	h.ServeHTTP(rec, req)

	assert.False(t, called, "handler must not run when bearer token is malformed")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// An invalid token that reaches the middleware must leave no token bytes and
// no claim or header value in the logs, even at debug level, and the client
// only sees the fixed error body. Not parallel: it swaps the default logger.
func TestMiddleware_InvalidToken_LogsNeitherTokenNorClaimValues(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	mock, v, aud := newMiddlewareValidator(t)
	foreign, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(foreign.Close)
	require.NoError(t, foreign.RotateKey(), "RotateKey") // signs under test-key-2, unknown to mock

	const (
		sub   = "sub-value-5b7d"
		zid   = "zid-value-81f0"
		iss   = "https://iss-value-3c9e.example"
		badAu = "aud-value-d24a"
	)
	extra := map[string]any{"zid": zid}
	sign := func(b *auth.TokenBuilder) string {
		t.Helper()
		tok, err := b.Claims(extra).Sign()
		require.NoError(t, err, "Sign")
		return tok
	}
	cases := []struct {
		name  string
		token string
	}{
		{"wrong_audience", sign(mock.Token(sub, badAu, mock.Server.URL))},
		{"wrong_issuer", sign(mock.Token(sub, aud, iss))},
		{"expired", sign(mock.Token(sub, aud, mock.Server.URL).Claim("exp", time.Now().Add(-time.Hour).Unix()))},
		{"unknown_kid", sign(foreign.Token(sub, aud, mock.Server.URL))},
		{"alg_hs256", sign(mock.Token(sub, aud, mock.Server.URL).Alg(jwa.HS256()))},
		{"missing_kid", sign(mock.Token(sub, aud, mock.Server.URL).WithoutKeyID())},
		{"garbage", "garbage-token-value-6e0b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			called := false
			h := auth.Middleware(v)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header.Set("Authorization", "bearer "+tc.token)

			h.ServeHTTP(rec, req)

			require.False(t, called, "the handler must not run")
			require.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.JSONEq(t, `{"error":"unauthorized","message":"invalid token"}`, rec.Body.String())
			out := logs.String()
			require.Contains(t, out, "auth validate failed", "the rejection is logged at debug")
			for _, part := range strings.Split(tc.token, ".") {
				if part != "" {
					assert.NotContains(t, out, part, "no token bytes in the logs")
				}
			}
			for _, value := range []string{sub, zid, iss, badAu, aud, mock.Server.URL, foreign.Server.URL, "test-key-"} {
				assert.NotContains(t, out, value, "no claim or header value in the logs")
			}
		})
	}
}

func TestMiddleware_MissingToken_RespondsWithFixedBody(t *testing.T) {
	t.Parallel()

	_, v, _ := newMiddlewareValidator(t)
	h := auth.Middleware(v)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer "} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}

		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code, "header %q", header)
		assert.JSONEq(t, `{"error":"unauthorized","message":"missing bearer token"}`, rec.Body.String(), "header %q", header)
	}
}

func TestMiddleware_AcceptsLowerCaseBearerScheme(t *testing.T) {
	t.Parallel()

	mock, v, aud := newMiddlewareValidator(t)
	tok, err := mock.Issue("u-1", aud, mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")
	called := false
	h := auth.Middleware(v)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "  bearer   "+tok+" ")

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)
}
