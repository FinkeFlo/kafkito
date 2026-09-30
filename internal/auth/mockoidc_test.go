// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
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
