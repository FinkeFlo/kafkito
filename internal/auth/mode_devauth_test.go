//go:build devauth

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestBuildValidator_OffMode_Devauth(t *testing.T) {
	t.Parallel()

	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "off"})
	require.NoError(t, err, "BuildValidator(off) under -tags devauth")
	t.Cleanup(cleanup)
	require.NotNil(t, v, "validator must not be nil under off mode")

	// In off mode the validator must accept any (or empty) token and stamp a synthetic principal.
	p, err := v.Validate(context.Background(), "ignored")

	require.NoError(t, err, "Validate under off mode must not error")
	require.NotNil(t, p, "synthetic principal must not be nil")
	assert.NotEmpty(t, p.Subject, "synthetic principal must have a non-empty Subject")
}
