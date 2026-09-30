// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// alg=none JWT with sub=attacker — must be rejected by Validate. Public-test
// payload, no real secret material. Gitleaks flags the high-entropy literal.
// header: {"alg":"none","typ":"JWT"} -> base64url
// payload: {"sub":"attacker","aud":"test-audience"} -> base64url
const algNoneToken = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + // gitleaks:allow
	"eyJzdWIiOiJhdHRhY2tlciIsImF1ZCI6InRlc3QtYXVkaWVuY2UifQ." // gitleaks:allow

// newOIDCValidator returns a generic mock issuer (no scope prefix, no zid)
// wired up to a freshly constructed OIDCValidator. The audience is fixed to
// "test-audience" so callers can mint matching/mismatching tokens.
func newOIDCValidator(t *testing.T, opts ...auth.MockOIDCOption) (*auth.MockOIDC, *auth.OIDCValidator, string) {
	t.Helper()

	mock, err := auth.NewMockOIDC(opts...)
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)

	const audience = "test-audience"
	v, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    mock.Server.URL,
		Audience:     audience,
		JWKSEndpoint: mock.JKU(),
	})
	require.NoError(t, err, "NewOIDCValidator")
	t.Cleanup(v.Close)

	return mock, v, audience
}

func TestOIDCValidator_HappyPath_AcceptsScopeStringClaim(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("user-123", aud, mock.Server.URL, []string{"read", "write"}, nil)
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.Equal(t, "user-123", p.Subject)
	assert.True(t, p.HasScope("read"), "HasScope(read); got scopes=%v", p.Scopes)
	assert.True(t, p.HasScope("write"), "HasScope(write); got scopes=%v", p.Scopes)
}

func TestOIDCValidator_HappyPath_AcceptsScopesArrayClaim(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	// Issue without scope claim, then layer "scopes" array via extra. We also
	// blank out the default "scope" claim so only "scopes" carries the data.
	tok, err := mock.Issue("user-arr", aud, mock.Server.URL, nil, map[string]any{
		"scopes": []string{"admin", "viewer"},
	})
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.True(t, p.HasScope("admin"), "HasScope(admin); got scopes=%v", p.Scopes)
	assert.True(t, p.HasScope("viewer"), "HasScope(viewer); got scopes=%v", p.Scopes)
}

func TestNewOIDCValidator_RejectsMissingConfigFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		cfg           auth.OIDCConfig
		wantErrSubstr string
	}{
		{
			name:          "missing_issuer_url",
			cfg:           auth.OIDCConfig{Audience: "a", JWKSEndpoint: "https://x/jwks"},
			wantErrSubstr: "IssuerURL required",
		},
		{
			name:          "missing_audience",
			cfg:           auth.OIDCConfig{IssuerURL: "https://x", JWKSEndpoint: "https://x/jwks"},
			wantErrSubstr: "Audience required",
		},
		{
			name:          "missing_jwks_endpoint",
			cfg:           auth.OIDCConfig{IssuerURL: "https://x", Audience: "a"},
			wantErrSubstr: "JWKSEndpoint required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := auth.NewOIDCValidator(tc.cfg)

			require.Error(t, err, "NewOIDCValidator must reject %s", tc.name)
			assert.ErrorContains(t, err, tc.wantErrSubstr,
				"error must name the missing field so operators can fix the misconfiguration")
		})
	}
}

// The issuer must match IssuerURL exactly: no sub-path, no trailing-slash
// normalization.
func TestOIDCValidator_RejectsIssuerThatIsNotAnExactMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		issuer func(base string) string
	}{
		{name: "tenant_sub_path", issuer: func(base string) string { return base + "/tenant-42" }},
		{name: "trailing_slash", issuer: func(base string) string { return base + "/" }},
		{name: "prefix_without_boundary", issuer: func(base string) string { return base + "0" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock, v, aud := newOIDCValidator(t)
			tok, err := mock.Issue("user-mt", aud, tc.issuer(mock.Server.URL), []string{"read"}, nil)
			require.NoError(t, err, "Issue")

			_, err = v.Validate(context.Background(), tok)

			require.ErrorIs(t, err, auth.ErrIssuerMismatch, "Validate must reject an iss that is not exactly IssuerURL")
			assertNoClaimValues(t, err, mock.Server.URL)
		})
	}
}

func TestOIDCValidator_TrailingSlashInIssuerURLMustMatchExactly(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)
	v, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    mock.Server.URL + "/",
		Audience:     "aud",
		JWKSEndpoint: mock.JKU(),
	})
	require.NoError(t, err, "NewOIDCValidator")
	t.Cleanup(v.Close)

	withSlash, err := mock.Issue("u", "aud", mock.Server.URL+"/", nil, nil)
	require.NoError(t, err, "Issue")
	withoutSlash, err := mock.Issue("u", "aud", mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")

	_, err = v.Validate(context.Background(), withSlash)
	require.NoError(t, err, "the configured issuer with its slash is accepted")
	_, err = v.Validate(context.Background(), withoutSlash)
	require.ErrorIs(t, err, auth.ErrIssuerMismatch, "the issuer without the slash is a different issuer")
}

// Keys published without "alg" are usable: the algorithm is inferred from the
// key type and must match the token header.
func TestOIDCValidator_KeyWithoutAlg_AcceptsAsymmetricAlgorithms(t *testing.T) {
	t.Parallel()

	for _, alg := range []jwa.SignatureAlgorithm{jwa.RS256(), jwa.RS512(), jwa.PS256()} {
		t.Run(alg.String(), func(t *testing.T) {
			t.Parallel()

			mock, v, aud := newOIDCValidator(t, auth.WithoutJWKAlg())
			tok, err := mock.Token("u", aud, mock.Server.URL).Alg(alg).Sign()
			require.NoError(t, err, "Sign")

			p, err := v.Validate(context.Background(), tok)

			require.NoError(t, err, "Validate")
			assert.Equal(t, "u", p.Subject)
		})
	}
}

// Algorithm pinning: HMAC and none are never accepted, and a key's own alg
// wins over the token header.
func TestOIDCValidator_RejectsAlgorithmOutsidePolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		keyAlg   bool // the JWKS key carries alg=RS256
		alg      jwa.SignatureAlgorithm
		rawToken string
	}{
		{name: "hs256_with_public_key", keyAlg: true, alg: jwa.HS256()},
		{name: "hs256_with_public_key_key_without_alg", keyAlg: false, alg: jwa.HS256()},
		{name: "hs512_with_public_key_key_without_alg", keyAlg: false, alg: jwa.HS512()},
		{name: "header_alg_differs_from_key_alg", keyAlg: true, alg: jwa.RS512()},
		{name: "header_ps256_key_rs256", keyAlg: true, alg: jwa.PS256()},
		{name: "alg_none", keyAlg: true, rawToken: algNoneToken},
		{name: "alg_none_key_without_alg", keyAlg: false, rawToken: algNoneToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var opts []auth.MockOIDCOption
			if !tc.keyAlg {
				opts = append(opts, auth.WithoutJWKAlg())
			}
			mock, v, aud := newOIDCValidator(t, opts...)
			tok := tc.rawToken
			if tok == "" {
				var err error
				tok, err = mock.Token("u", aud, mock.Server.URL).Alg(tc.alg).Sign()
				require.NoError(t, err, "Sign")
			}

			_, err := v.Validate(context.Background(), tok)

			require.Error(t, err, "Validate must reject the algorithm")
			assert.ErrorContains(t, err, "token alg", "the algorithm policy must reject it")
		})
	}
}

func TestOIDCValidator_RejectsInvalidToken(t *testing.T) {
	t.Parallel()

	// tokenForRow builds the token for a row. Returning nil means the row
	// validates a literal raw string (rawToken) instead of a freshly issued
	// JWT — used for malformed and alg=none cases.
	type tokenIssuer func(t *testing.T, mock *auth.MockOIDC, aud string) string

	cases := []struct {
		name     string
		issue    tokenIssuer
		rawToken string
		wantErr  error
	}{
		{
			name: "wrong_audience",
			issue: func(t *testing.T, mock *auth.MockOIDC, _ string) string {
				t.Helper()
				tok, err := mock.Issue("u", "other-audience", mock.Server.URL, []string{"read"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErr: auth.ErrAudienceMismatch,
		},
		{
			name: "wrong_issuer",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Issue("u", aud, "https://evil.example", []string{"read"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErr: auth.ErrIssuerMismatch,
		},
		{
			// Distinct from wrong_audience: the aud claim is empty, not
			// merely mismatched. Locks oidc_validator.go:97 (len(auds)==0)
			// separately from oidc_validator.go:99-100 (AudienceContains
			// false).
			name: "aud_claim_missing",
			issue: func(t *testing.T, mock *auth.MockOIDC, _ string) string {
				t.Helper()
				tok, err := mock.Issue("u", "ignored", mock.Server.URL,
					[]string{"read"}, map[string]any{"aud": []string{}})
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErr: auth.ErrAudienceMissing,
		},
		{
			name: "expired_token",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				past := time.Now().Add(-10 * time.Minute).Unix()
				tok, err := mock.Issue("u", aud, mock.Server.URL, []string{"read"}, map[string]any{
					"exp": past,
					"iat": time.Now().Add(-20 * time.Minute).Unix(),
				})
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErr: auth.ErrTokenExpired,
		},
		{
			name: "missing_exp",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Token("u", aud, mock.Server.URL).Without(jwt.ExpirationKey).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErr: auth.ErrExpMissing,
		},
		{
			name: "missing_sub",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Token("u", aud, mock.Server.URL).Without(jwt.SubjectKey).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErr: auth.ErrSubMissing,
		},
		{
			name: "empty_sub",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Token("", aud, mock.Server.URL).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErr: auth.ErrSubMissing,
		},
		{
			name: "missing_kid",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Token("u", aud, mock.Server.URL).WithoutKeyID().Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErr: auth.ErrKidMissing,
		},
		{
			name: "nbf_in_the_future",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Token("u", aud, mock.Server.URL).
					Claim(jwt.NotBeforeKey, time.Now().Add(10*time.Minute).Unix()).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErr: auth.ErrTokenNotYetValid,
		},
		{
			name:     "malformed_token_garbage",
			rawToken: "not.a.jwt",
			wantErr:  auth.ErrTokenMalformed,
		},
		{
			name:     "malformed_token_empty",
			rawToken: "",
			wantErr:  auth.ErrTokenEmpty,
		},
		{
			name:     "alg_none_unsigned",
			rawToken: algNoneToken,
			wantErr:  auth.ErrAlgNotAllowed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock, v, aud := newOIDCValidator(t)
			tok := tc.rawToken
			if tc.issue != nil {
				tok = tc.issue(t, mock, aud)
			}

			_, err := v.Validate(context.Background(), tok)

			require.ErrorIs(t, err, tc.wantErr, "Validate must reject this token for the row's reason")
			assertNoClaimValues(t, err, mock.Server.URL, aud, "other-audience", "evil.example", "test-key-")
		})
	}
}

// Replicates the production race of #100: the first requests after a start
// arrive together at a fresh validator. All must pass, with one JWKS fetch.
func TestOIDCValidator_ConcurrentFirstRequests_AllSucceed(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("user-1", aud, mock.Server.URL, []string{"read"}, nil)
	require.NoError(t, err, "Issue")

	const requests = 32
	start := make(chan struct{})
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			<-start
			_, err := v.Validate(context.Background(), tok)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "no first request may fail")
	}
	assert.Equal(t, int64(1), mock.JWKSRequests(), "concurrent first requests must share one JWKS fetch")
}

func TestOIDCValidator_WarmUp_LoadsTheKeysBeforeTheFirstRequest(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	require.NoError(t, v.WarmUp(context.Background()), "WarmUp")
	require.Equal(t, int64(1), mock.JWKSRequests())
	tok, err := mock.Issue("user-1", aud, mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")

	_, err = v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.Equal(t, int64(1), mock.JWKSRequests(), "Validate must use the warmed-up keys")
}

func TestOIDCValidator_IdPDown_FailsFastInsteadOfHanging(t *testing.T) {
	t.Parallel()

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	mock, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)
	v, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    mock.Server.URL,
		Audience:     "test-audience",
		JWKSEndpoint: down.URL + "/jwks",
	})
	require.NoError(t, err, "NewOIDCValidator")
	t.Cleanup(v.Close)
	tok, err := mock.Issue("u", "test-audience", mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")

	require.ErrorIs(t, v.WarmUp(context.Background()), auth.ErrKeysUnavailable, "WarmUp reports the outage")
	begin := time.Now()
	_, err = v.Validate(context.Background(), tok)

	require.ErrorIs(t, err, auth.ErrKeysUnavailable)
	assert.Less(t, time.Since(begin), time.Second, "a request after a failed fetch must not wait")
}

func TestOIDCValidator_Close_RejectsLaterTokens(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("u", aud, mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")

	v.Close()
	_, err = v.Validate(context.Background(), tok)

	require.ErrorIs(t, err, auth.ErrKeySourceClosed)
}

// A token signed by a key the JWKS does not hold fails verification. jwx
// names the kid in such errors, so Validate must not pass its text on.
func TestOIDCValidator_RejectsForeignKey_WithoutNamingTheKid(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	other, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(other.Close)
	require.NoError(t, other.RotateKey(), "RotateKey") // other now signs with test-key-2
	tok, err := other.Issue("secret-subject", aud, mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")

	_, err = v.Validate(context.Background(), tok)

	require.ErrorIs(t, err, auth.ErrTokenInvalid)
	assertNoClaimValues(t, err, "secret-subject", mock.Server.URL, other.Server.URL, aud, "test-key-")
}

// A tampered payload keeps kid and alg, so only the signature check fails.
func TestOIDCValidator_RejectsTamperedSignature(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("u", aud, mock.Server.URL, nil, nil)
	require.NoError(t, err, "Issue")
	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)

	_, err = v.Validate(context.Background(), tampered)

	require.ErrorIs(t, err, auth.ErrTokenInvalid)
}

// assertNoClaimValues fails when err's text carries any of values.
func assertNoClaimValues(t *testing.T, err error, values ...string) {
	t.Helper()

	for _, v := range values {
		assert.NotContains(t, err.Error(), v, "the error must name the rule, not a claim or header value")
	}
}
