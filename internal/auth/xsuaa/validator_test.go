//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

// newValidator starts a mock XSUAA (key set at /token_keys) and a validator
// that accepts the mock's plain-http loopback jku.
func newValidator(t *testing.T, opts ...auth.MockOIDCOption) (*auth.MockOIDC, *xsuaa.Validator, xsuaa.Credentials) {
	t.Helper()

	mock, err := auth.NewMockOIDC(append([]auth.MockOIDCOption{
		auth.WithScopePrefix("kafkito!t12345"),
		auth.WithZoneID("test-zone"),
		auth.WithJWKSPath("/token_keys"),
	}, opts...)...)
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)

	creds := xsuaa.Credentials{
		ClientID:       "sb-kafkito!t12345",
		URL:            mock.Server.URL,
		UAADomain:      mock.Host(), // bare hostname (no port) for the UAADomain allow-list
		XSAppName:      "kafkito!t12345",
		IdentityZoneID: "test-zone",
	}
	v, err := xsuaa.NewValidatorForTest(creds)
	require.NoError(t, err, "NewValidatorForTest")
	t.Cleanup(v.Close)

	return mock, v, creds
}

func TestValidator_HappyPath_AcceptsCanonicalToken(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	tok, err := mock.Issue("u-1", creds.ClientID, creds.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	require.NotNil(t, p)
	assert.Equal(t, "u-1", p.Subject)
	assert.True(t, p.HasScope("Display"), "HasScope(Display); scopes=%v", p.Scopes)
}

func TestValidator_HappyPath_AcceptsAudienceViaXSAppName(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	// Issue with aud = xsappname (no sb- prefix), simulating an XSUAA flow that targets the app directly.
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
		"aud": []string{creds.XSAppName},
	})
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	require.NotNil(t, p)
	assert.Equal(t, "u", p.Subject)
}

// XSUAA sets sub to the client id in client-credentials tokens, which carry
// no user_name; such tokens stay valid now that sub is required.
func TestValidator_AcceptsClientCredentialsToken(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	tok, err := mock.Token(creds.ClientID, creds.ClientID, creds.URL).
		Scopes("Display").
		Claim("grant_type", "client_credentials").
		Sign()
	require.NoError(t, err, "Sign")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.Equal(t, creds.ClientID, p.Subject)
}

// xsuaa keeps its issuer rule: the credentials url or a path under it.
func TestValidator_AcceptsIssuerUnderTheCredentialsURL(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{"/", "/oauth/token"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()

			mock, v, creds := newValidator(t)
			tok, err := mock.Issue("u", creds.ClientID, creds.URL+suffix, []string{"Display"}, nil)
			require.NoError(t, err, "Issue")

			_, err = v.Validate(context.Background(), tok)

			require.NoError(t, err, "Validate")
		})
	}
}

// Keys published without "alg" are usable with an inferred asymmetric alg.
func TestValidator_KeyWithoutAlg_AcceptsAsymmetricAlgorithms(t *testing.T) {
	t.Parallel()

	for _, alg := range []jwa.SignatureAlgorithm{jwa.RS256(), jwa.PS384()} {
		t.Run(alg.String(), func(t *testing.T) {
			t.Parallel()

			mock, v, creds := newValidator(t, auth.WithoutJWKAlg())
			tok, err := mock.Token("u", creds.ClientID, creds.URL).Scopes("Display").Alg(alg).Sign()
			require.NoError(t, err, "Sign")

			p, err := v.Validate(context.Background(), tok)

			require.NoError(t, err, "Validate")
			assert.Equal(t, "u", p.Subject)
		})
	}
}

// Algorithm pinning: HMAC is never accepted, and a key's own alg wins over
// the token header.
func TestValidator_RejectsAlgorithmOutsidePolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		keyAlg bool // the JWKS key carries alg=RS256
		alg    jwa.SignatureAlgorithm
	}{
		{name: "hs256_with_public_key", keyAlg: true, alg: jwa.HS256()},
		{name: "hs256_with_public_key_key_without_alg", keyAlg: false, alg: jwa.HS256()},
		{name: "header_alg_differs_from_key_alg", keyAlg: true, alg: jwa.RS384()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var opts []auth.MockOIDCOption
			if !tc.keyAlg {
				opts = append(opts, auth.WithoutJWKAlg())
			}
			mock, v, creds := newValidator(t, opts...)
			tok, err := mock.Token("u", creds.ClientID, creds.URL).Scopes("Display").Alg(tc.alg).Sign()
			require.NoError(t, err, "Sign")

			_, err = v.Validate(context.Background(), tok)

			require.ErrorContains(t, err, "token alg", "the algorithm policy must reject it")
		})
	}
}

func TestValidator_RejectsInvalidToken(t *testing.T) {
	t.Parallel()

	// mutateCreds: optional callback to alter the Credentials before building the
	// validator. Used by rows that need a non-default validator (e.g. wrong UAADomain).
	// issueToken: produces the JWT (or post-Issue tampered string) under test for the row.
	type tokenIssuer func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string

	cases := []struct {
		name             string
		mutateCreds      func(*xsuaa.Credentials)
		issueToken       tokenIssuer
		wantErrSubstring string
	}{
		{
			name: "wrong_issuer",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, "https://evil.example", []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "not under",
		},
		{
			// Upstream library wording for audience errors isn't part of our public
			// contract, so we only require that an error is returned.
			name: "wrong_audience",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", "sb-other!t12345", creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
		},
		{
			name: "bad_jku_host",
			mutateCreds: func(c *xsuaa.Credentials) {
				c.UAADomain = "different.example.com"
			},
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				// creds passed here already carries the original UAADomain; the
				// mutated copy is used only for validator construction.
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "jku",
		},
		{
			name: "missing_jku",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				raw, err := mock.IssueWithoutJKU("u-1", creds.ClientID, creds.URL, []string{"Display"})
				require.NoError(t, err, "IssueWithoutJKU")
				return raw
			},
			wantErrSubstring: "jku",
		},
		{
			// Signature error wording isn't substring-stable across upstream versions.
			name: "tampered_signature",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")

				// JWS compact form is "<header>.<payload>.<signature>". Mutate one
				// byte in the decoded signature so the signature is guaranteed
				// different from any valid one.
				parts := strings.Split(tok, ".")
				require.Len(t, parts, 3, "expected 3 JWS parts")
				sig, err := base64.RawURLEncoding.DecodeString(parts[2])
				require.NoError(t, err, "decode signature")
				require.GreaterOrEqual(t, len(sig), 8, "signature suspiciously short")
				sig[len(sig)/2] ^= 0xFF // flip every bit of one middle byte
				parts[2] = base64.RawURLEncoding.EncodeToString(sig)
				return strings.Join(parts, ".")
			},
		},
		{
			name: "expired_token",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				past := time.Now().Add(-5 * time.Minute).Unix()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
					"exp": past,
					"iat": time.Now().Add(-10 * time.Minute).Unix(),
				})
				require.NoError(t, err, "Issue")
				return tok
			},
		},
		{
			name: "missing_exp",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Token("u", creds.ClientID, creds.URL).Scopes("Display").
					Without(jwt.ExpirationKey).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErrSubstring: "exp",
		},
		{
			name: "missing_sub",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Token("u", creds.ClientID, creds.URL).Scopes("Display").
					Without(jwt.SubjectKey).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErrSubstring: "sub",
		},
		{
			name: "empty_sub",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Token("", creds.ClientID, creds.URL).Scopes("Display").Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErrSubstring: "sub",
		},
		{
			name: "missing_kid",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Token("u", creds.ClientID, creds.URL).Scopes("Display").WithoutKeyID().Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErrSubstring: "no kid",
		},
		{
			// Unsigned token whose header names the mock's own jku and kid,
			// so it reaches the algorithm check.
			name: "alg_none",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				hdr, err := json.Marshal(map[string]string{"alg": "none", "kid": "test-key-1", "jku": mock.JKU()})
				require.NoError(t, err)
				payload, err := json.Marshal(map[string]any{
					"sub": "u", "iss": creds.URL, "aud": []string{creds.ClientID},
					"exp": time.Now().Add(time.Hour).Unix(), "zid": creds.IdentityZoneID,
				})
				require.NoError(t, err)
				return base64.RawURLEncoding.EncodeToString(hdr) + "." +
					base64.RawURLEncoding.EncodeToString(payload) + "."
			},
			wantErrSubstring: "token alg",
		},
		{
			name: "nbf_in_the_future",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Token("u", creds.ClientID, creds.URL).Scopes("Display").
					Claim(jwt.NotBeforeKey, time.Now().Add(10*time.Minute).Unix()).Sign()
				require.NoError(t, err, "Sign")
				return tok
			},
			wantErrSubstring: "nbf",
		},
		{
			name: "wrong_zid",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
					"zid": "different-zone",
				})
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "zid",
		},
		{
			// UAADomain="ost" must not match mock.Host() == "127.0.0.1" by suffix.
			name: "hostname_substring_match",
			mutateCreds: func(c *xsuaa.Credentials) {
				c.UAADomain = "ost"
			},
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "jku",
		},
		{
			// iss = creds.URL + ".evil.com" — strings.HasPrefix would match without the / boundary check.
			name: "issuer_suffix_attack",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				evilIss := creds.URL + ".evil.com"
				tok, err := mock.Issue("u", creds.ClientID, evilIss, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "not under",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock, v, creds := newValidator(t)
			if tc.mutateCreds != nil {
				mutated := creds
				tc.mutateCreds(&mutated)
				var err error
				v, err = xsuaa.NewValidatorForTest(mutated)
				require.NoError(t, err, "NewValidatorForTest(mutated)")
				t.Cleanup(v.Close)
			}
			tok := tc.issueToken(t, mock, creds)

			_, err := v.Validate(context.Background(), tok)

			require.Error(t, err, "Validate must reject")
			if tc.wantErrSubstring != "" {
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			}
		})
	}
}

func TestNewValidator_RejectsUAADomainWithPort(t *testing.T) {
	t.Parallel()

	_, err := xsuaa.NewValidator(xsuaa.Credentials{
		ClientID: "x", URL: "https://x.example", UAADomain: "x.example:8080", XSAppName: "x",
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "UAADomain")
}

func TestValidator_RejectsJKUOutsidePolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		jku              func(mock *auth.MockOIDC) string
		wantErrSubstring string
	}{
		{
			name:             "other_domain",
			jku:              func(*auth.MockOIDC) string { return "https://evil.example/token_keys" },
			wantErrSubstring: "uaadomain",
		},
		{
			name:             "wrong_path",
			jku:              func(m *auth.MockOIDC) string { return m.Server.URL + "/jwks" },
			wantErrSubstring: "jku path",
		},
		{
			name:             "path_prefix",
			jku:              func(m *auth.MockOIDC) string { return m.Server.URL + "/token_keys/x" },
			wantErrSubstring: "jku path",
		},
		{
			name:             "query",
			jku:              func(m *auth.MockOIDC) string { return m.JKU() + "?tenant=x" },
			wantErrSubstring: "query",
		},
		{
			name:             "fragment",
			jku:              func(m *auth.MockOIDC) string { return m.JKU() + "#x" },
			wantErrSubstring: "fragment",
		},
		{
			name: "user_info",
			jku: func(m *auth.MockOIDC) string {
				return strings.Replace(m.JKU(), "http://", "http://user@", 1)
			},
			wantErrSubstring: "user info",
		},
		{
			name:             "relative",
			jku:              func(*auth.MockOIDC) string { return "/token_keys" },
			wantErrSubstring: "jku is not an absolute URL",
		},
		{
			// A zone literal suffix-matches the uaadomain but dials the IP.
			name: "ipv6_zone_literal",
			jku: func(m *auth.MockOIDC) string {
				return "https://[::ffff:" + m.Host() + "%25x." + m.Host() + "]:" + mockPort(m) + "/token_keys"
			},
			wantErrSubstring: "DNS name",
		},
		{
			name: "ipv6_zone_literal_double_escaped",
			jku: func(m *auth.MockOIDC) string {
				return "https://[::ffff:" + m.Host() + "%2525x." + m.Host() + "]:" + mockPort(m) + "/token_keys"
			},
			wantErrSubstring: "DNS name",
		},
		{
			name: "ipv4_mapped_ipv6_literal",
			jku: func(m *auth.MockOIDC) string {
				return "https://[::ffff:" + m.Host() + "]:" + mockPort(m) + "/token_keys"
			},
			wantErrSubstring: "DNS name",
		},
		{
			name:             "ipv4_literal_https",
			jku:              func(m *auth.MockOIDC) string { return "https://" + m.Host() + ":" + mockPort(m) + "/token_keys" },
			wantErrSubstring: "DNS name",
		},
		{
			// Loopback http is the test exception; any other http host is not.
			name:             "http_non_loopback",
			jku:              func(*auth.MockOIDC) string { return "http://auth.example/token_keys" },
			wantErrSubstring: "https",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock, v, creds := newValidator(t)
			tok, err := mock.IssueWithJKU(tc.jku(mock), "u", creds.ClientID, creds.URL, []string{"Display"})
			require.NoError(t, err, "IssueWithJKU")

			_, err = v.Validate(context.Background(), tok)

			require.ErrorContains(t, err, tc.wantErrSubstring, "Validate must reject the jku")
			assert.Zero(t, mock.JWKSRequests(), "a rejected jku must not be fetched")
		})
	}
}

// mockPort returns the port the mock XSUAA listens on.
func mockPort(m *auth.MockOIDC) string {
	u, _ := url.Parse(m.Server.URL)
	return u.Port()
}

func TestNewValidator_RejectsPlainHTTPJKU(t *testing.T) {
	t.Parallel()

	mock, _, creds := newValidator(t)
	v, err := xsuaa.NewValidator(creds) // production constructor: https only
	require.NoError(t, err, "NewValidator")
	t.Cleanup(v.Close)
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	_, err = v.Validate(context.Background(), tok)

	require.ErrorContains(t, err, "https")
	assert.Zero(t, mock.JWKSRequests())
}

// Replicates the production race of #100 (2 of 4 parallel first requests got
// a 401 after a restart): all first requests must pass, with one fetch.
func TestValidator_ConcurrentFirstRequests_AllSucceed(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	tok, err := mock.Issue("u-1", creds.ClientID, creds.URL, []string{"Display"}, nil)
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

func TestValidator_WarmUp_LoadsTheOwnTenantKeys(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	require.NoError(t, v.WarmUp(context.Background()), "WarmUp")
	require.Equal(t, int64(1), mock.JWKSRequests())
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	_, err = v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.Equal(t, int64(1), mock.JWKSRequests(), "the token's jku must hit the warmed-up source")
}

func TestValidator_Close_RejectsLaterTokens(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	v.Close()
	_, err = v.Validate(context.Background(), tok)

	require.ErrorIs(t, err, auth.ErrKeySourceClosed)
}

// NewMode keeps starting when the warm-up fails (here: the production
// validator rejects the mock's http URL) and returns a real cleanup.
func TestNewMode_WarmUpFailure_StillReturnsValidatorAndCleanup(t *testing.T) {
	t.Parallel()

	mock, _, creds := newValidator(t)
	vcap, err := json.Marshal(map[string]any{
		"xsuaa": []map[string]any{{"credentials": creds}},
	})
	require.NoError(t, err)

	v, cleanup, err := xsuaa.NewMode(auth.ModeConfig{Mode: "xsuaa", VCAPServices: string(vcap)})

	require.NoError(t, err, "a failed warm-up must not fail startup")
	require.NotNil(t, v)
	require.NotNil(t, cleanup, "xsuaa must return a real cleanup")
	cleanup()
	_, err = v.Validate(context.Background(), "not.a.jwt")
	require.Error(t, err)
	assert.Zero(t, mock.JWKSRequests(), "the http warm-up URL must be rejected before any fetch")
}
