// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// A JWS in JSON serialization may carry several signatures. ParseToken
// checks the alg of one signature only, so it rejects any other count
// itself instead of relying on jwt.Parse accepting compact tokens only.
func TestParseToken_RejectsMoreThanOneSignature(t *testing.T) {
	t.Parallel()

	priv, set := newRSAKeySet(t, true)
	tok := jwt.New()
	require.NoError(t, tok.Set(jwt.SubjectKey, "u"))
	require.NoError(t, tok.Set(jwt.ExpirationKey, time.Now().Add(time.Hour)))
	payload, err := jwt.NewSerializer().Serialize(tok)
	require.NoError(t, err)
	hdr := jws.NewHeaders()
	require.NoError(t, hdr.Set(jws.KeyIDKey, "k1"))
	signed, err := jws.Sign(payload, jws.WithJSON(),
		jws.WithKey(jwa.RS256(), priv, jws.WithProtectedHeaders(hdr)),
		jws.WithKey(jwa.RS256(), priv, jws.WithProtectedHeaders(hdr)),
	)
	require.NoError(t, err)

	_, err = auth.ParseToken(string(signed), set)

	require.ErrorIs(t, err, auth.ErrTokenSignatures)
}

// newRSAKeySet returns a new RSA key and a set with its public key under
// kid "k1", with alg=RS256 when withAlg is set.
func newRSAKeySet(t *testing.T, withAlg bool) (*rsa.PrivateKey, jwk.Set) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pub, err := jwk.Import(&priv.PublicKey)
	require.NoError(t, err)
	require.NoError(t, pub.Set(jwk.KeyIDKey, "k1"))
	if withAlg {
		require.NoError(t, pub.Set(jwk.AlgorithmKey, jwa.RS256()))
	}
	set := jwk.NewSet()
	require.NoError(t, set.AddKey(pub))
	return priv, set
}

// jwt.Parse also verifies a JWS nested in the payload. Its signature would
// skip the alg policy, so ParseToken accepts a JSON claim set only.
func TestParseToken_RejectsNestedToken(t *testing.T) {
	t.Parallel()

	priv, set := newRSAKeySet(t, false)
	hdr := jws.NewHeaders()
	require.NoError(t, hdr.Set(jws.KeyIDKey, "k1"))
	tok := jwt.New()
	require.NoError(t, tok.Set(jwt.SubjectKey, "u"))
	require.NoError(t, tok.Set(jwt.ExpirationKey, time.Now().Add(time.Hour)))
	inner, err := jwt.Sign(tok, jwt.WithKey(jwa.PS512(), priv, jws.WithProtectedHeaders(hdr)))
	require.NoError(t, err)
	outer, err := jws.Sign(inner, jws.WithKey(jwa.RS256(), priv, jws.WithProtectedHeaders(hdr)))
	require.NoError(t, err)

	_, err = auth.ParseToken(string(outer), set)

	require.ErrorIs(t, err, auth.ErrNestedToken)
}

// jwx names the kid when a key is not usable for signatures. ParseToken
// reports the rule only.
func TestParseToken_KeyNotForSignatures_DoesNotNameTheKid(t *testing.T) {
	t.Parallel()

	const kid = "distinctive-kid-7f3a"
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pub, err := jwk.Import(&priv.PublicKey)
	require.NoError(t, err)
	require.NoError(t, pub.Set(jwk.KeyIDKey, kid))
	require.NoError(t, pub.Set(jwk.KeyUsageKey, jwk.ForEncryption))
	set := jwk.NewSet()
	require.NoError(t, set.AddKey(pub))
	raw := signRS256(t, priv, kid, map[string]any{jwt.SubjectKey: "u", jwt.ExpirationKey: time.Now().Add(time.Hour)})

	_, err = auth.ParseToken(raw, set)

	require.ErrorIs(t, err, auth.ErrTokenInvalid)
	assert.NotContains(t, err.Error(), kid)
}

func TestParseToken_MapsTimeClaimFailuresToSentinels(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cases := []struct {
		name   string
		claims map[string]any
		want   error
	}{
		{"expired", map[string]any{jwt.ExpirationKey: now.Add(-time.Hour)}, auth.ErrTokenExpired},
		{"exp_missing", map[string]any{}, auth.ErrExpMissing},
		{"nbf_future", map[string]any{jwt.ExpirationKey: now.Add(time.Hour), jwt.NotBeforeKey: now.Add(time.Hour)}, auth.ErrTokenNotYetValid},
		{"iat_future", map[string]any{jwt.ExpirationKey: now.Add(time.Hour), jwt.IssuedAtKey: now.Add(time.Hour)}, auth.ErrTokenIssuedAt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			priv, set := newRSAKeySet(t, true)
			tc.claims[jwt.SubjectKey] = "u"

			_, err := auth.ParseToken(signRS256(t, priv, "k1", tc.claims), set)

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestParseToken_RejectsGarbageAsMalformed(t *testing.T) {
	t.Parallel()

	_, set := newRSAKeySet(t, true)

	_, err := auth.ParseToken("not.a.jwt", set)

	require.ErrorIs(t, err, auth.ErrTokenMalformed)
}

// signRS256 signs claims as a compact JWS under kid.
func signRS256(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()

	tok := jwt.New()
	for k, v := range claims {
		require.NoError(t, tok.Set(k, v))
	}
	hdr := jws.NewHeaders()
	require.NoError(t, hdr.Set(jws.KeyIDKey, kid))
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), priv, jws.WithProtectedHeaders(hdr)))
	require.NoError(t, err)
	return string(signed)
}
