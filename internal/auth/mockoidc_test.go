// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// tokenHeader returns the kid and jku of a compact JWS.
func tokenHeader(t *testing.T, raw string) (kid, jku string) {
	t.Helper()

	msg, err := jws.Parse([]byte(raw))
	require.NoError(t, err)
	require.NotEmpty(t, msg.Signatures())
	hdr := msg.Signatures()[0].ProtectedHeaders()
	kid, _ = hdr.KeyID()
	jku, _ = hdr.JWKSetURL()
	return kid, jku
}

func TestMockOIDC_RotateKey_ServesAndSignsWithOnlyTheNewKey(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	require.NoError(t, mock.RotateKey())

	set, err := jwk.Fetch(context.Background(), mock.JKU())
	require.NoError(t, err)
	require.Equal(t, 1, set.Len(), "the rotated-out key must be gone")
	_, ok := set.LookupKeyID("test-key-2")
	assert.True(t, ok, "the JWKS must serve the new kid")
	assert.Equal(t, int64(1), mock.JWKSRequests())

	tok, err := mock.Issue("u", "aud", mock.Server.URL, nil, nil)
	require.NoError(t, err)
	kid, _ := tokenHeader(t, tok)
	assert.Equal(t, "test-key-2", kid, "tokens after a rotation carry the new kid")
}

func TestMockOIDC_WithJWKSPath_ServesAndAdvertisesThePath(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC(auth.WithJWKSPath("/token_keys"))
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	require.True(t, strings.HasSuffix(mock.JKU(), "/token_keys"))
	_, err = jwk.Fetch(context.Background(), mock.JKU())
	require.NoError(t, err)

	tok, err := mock.Issue("u", "aud", mock.Server.URL, nil, nil)
	require.NoError(t, err)
	_, jku := tokenHeader(t, tok)
	assert.Equal(t, mock.JKU(), jku)
}

func TestMockOIDC_IssueWithJKU_SetsTheGivenHeader(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	tok, err := mock.IssueWithJKU("https://elsewhere.example/token_keys", "u", "aud", mock.Server.URL, nil)
	require.NoError(t, err)

	_, jku := tokenHeader(t, tok)
	assert.Equal(t, "https://elsewhere.example/token_keys", jku)
}

func TestMockOIDC_TokenBuilder_OmitsClaimsAndSetsTheAlgorithm(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	raw, err := mock.Token("u", "aud", mock.Server.URL).
		Without(jwt.ExpirationKey, jwt.SubjectKey).
		Claim("extra", "x").
		Alg(jwa.PS256()).
		Sign()
	require.NoError(t, err)

	msg, err := jws.Parse([]byte(raw))
	require.NoError(t, err)
	alg, _ := msg.Signatures()[0].ProtectedHeaders().Algorithm()
	assert.Equal(t, jwa.PS256(), alg)
	tok, err := jwt.ParseInsecure([]byte(raw))
	require.NoError(t, err)
	assert.False(t, tok.Has(jwt.ExpirationKey), "exp must be omitted")
	assert.False(t, tok.Has(jwt.SubjectKey), "sub must be omitted")
	assert.True(t, tok.Has("extra"))
	assert.True(t, tok.Has(jwt.IssuedAtKey), "other defaults stay")
}

func TestMockOIDC_WithoutJWKAlg_ServesTheKeyWithoutAlg(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC(auth.WithoutJWKAlg())
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	set, err := jwk.Fetch(context.Background(), mock.JKU())
	require.NoError(t, err)
	key, ok := set.Key(0)
	require.True(t, ok)
	_, hasAlg := key.Algorithm()
	assert.False(t, hasAlg, "the served key must not carry alg")
}

func TestMockOIDC_ServesDiscoveryMetadata(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	resp, err := http.Get(mock.Server.URL + "/.well-known/openid-configuration")
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var meta map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&meta))

	assert.Equal(t, mock.Server.URL, meta["issuer"])
	assert.Equal(t, mock.JKU(), meta["jwks_uri"])
}

func TestTokenBuilder_Type_SetsTheTypHeader(t *testing.T) {
	t.Parallel()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	withTyp, err := mock.Token("u", "a", "i").Type("at+jwt").Sign()
	require.NoError(t, err)
	withoutTyp, err := mock.Token("u", "a", "i").Type("").Sign()
	require.NoError(t, err)

	assert.Equal(t, "at+jwt", headerTyp(t, withTyp))
	assert.Empty(t, headerTyp(t, withoutTyp))
}

func headerTyp(t *testing.T, raw string) string {
	t.Helper()
	msg, err := jws.Parse([]byte(raw))
	require.NoError(t, err)
	typ, _ := msg.Signatures()[0].ProtectedHeaders().Type()
	return typ
}
