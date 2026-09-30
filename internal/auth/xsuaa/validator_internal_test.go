//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCapTestValidator(t *testing.T) *Validator {
	t.Helper()

	x, err := newValidator(Credentials{
		ClientID:  "sb-kafkito!t1",
		URL:       "https://provider.auth.example.com",
		UAADomain: "auth.example.com",
		XSAppName: "kafkito!t1",
	}, false)
	require.NoError(t, err)
	t.Cleanup(x.Close)
	return x
}

func TestValidator_OwnJKU_IsRegisteredAtConstruction(t *testing.T) {
	t.Parallel()

	x := newCapTestValidator(t)

	assert.Equal(t, "https://provider.auth.example.com/token_keys", x.ownJKU)
	assert.Len(t, x.sources, 1)
}

func TestValidator_CanonicalJKU_NormalizesSchemeAndHost(t *testing.T) {
	t.Parallel()

	x := newCapTestValidator(t)

	got, err := x.canonicalJKU("HTTPS://Provider.Auth.Example.com/token_keys")

	require.NoError(t, err)
	assert.Equal(t, x.ownJKU, got, "spelling variants must map to one cache entry")
}

func TestValidator_KeySource_CapsDistinctJKUs(t *testing.T) {
	t.Parallel()

	x := newCapTestValidator(t)

	// The own jku occupies one slot; 15 more fit.
	for i := range maxJKUs - 1 {
		_, err := x.keySource(fmt.Sprintf("https://t%d.auth.example.com/token_keys", i))
		require.NoError(t, err, "jku %d is within the cap", i)
	}
	_, err := x.keySource("https://one-too-many.auth.example.com/token_keys")
	require.ErrorIs(t, err, errJKUTooMany)

	_, err = x.keySource("https://t0.auth.example.com/token_keys")
	require.NoError(t, err, "known jku URLs keep working at the cap")
	_, err = x.keySource(x.ownJKU)
	require.NoError(t, err, "the own jku keeps working at the cap")
	assert.Len(t, x.sources, maxJKUs)
}
