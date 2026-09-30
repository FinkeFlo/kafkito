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

	require.ErrorContains(t, err, "exactly one signature")
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

	require.ErrorContains(t, err, "not a JSON object")
}
