// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A key rotation at the IdP must not lock users out: the first token with the
// new kid triggers one refresh once the refresh interval has passed.
func TestOIDCValidator_KeyRotation_RefreshesOnUnknownKid(t *testing.T) {
	t.Parallel()

	mock, err := NewMockOIDC()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	v, err := NewOIDCValidator(OIDCConfig{
		IssuerURL:    mock.Server.URL,
		Audience:     "aud",
		JWKSEndpoint: mock.JKU(),
	})
	require.NoError(t, err)
	t.Cleanup(v.Close)
	clk := newFakeClock()
	v.keys.now = clk.Now

	old, err := mock.Issue("u", "aud", mock.Server.URL, nil, nil)
	require.NoError(t, err)
	_, err = v.Validate(context.Background(), old)
	require.NoError(t, err, "token signed with the first key")

	require.NoError(t, mock.RotateKey())
	rotated, err := mock.Issue("u", "aud", mock.Server.URL, nil, nil)
	require.NoError(t, err)

	_, err = v.Validate(context.Background(), rotated)
	require.Error(t, err, "within the refresh interval the new kid is not fetched")
	assert.Equal(t, int64(1), mock.JWKSRequests())

	clk.Advance(time.Minute)
	_, err = v.Validate(context.Background(), rotated)
	require.NoError(t, err, "after the interval the unknown kid triggers a refresh")
	assert.Equal(t, int64(2), mock.JWKSRequests())

	_, err = v.Validate(context.Background(), old)
	require.Error(t, err, "the rotated-out key is no longer trusted")
}
