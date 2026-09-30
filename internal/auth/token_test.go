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

// A JWS in JSON serialization may carry several signatures; ParseToken
// accepts exactly one, so every signature it verifies passed the alg policy.
func TestParseToken_RejectsMoreThanOneSignature(t *testing.T) {
	t.Parallel()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pub, err := jwk.Import(&priv.PublicKey)
	require.NoError(t, err)
	require.NoError(t, pub.Set(jwk.KeyIDKey, "k1"))
	require.NoError(t, pub.Set(jwk.AlgorithmKey, jwa.RS256()))
	set := jwk.NewSet()
	require.NoError(t, set.AddKey(pub))

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
