//go:build !devauth

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

func TestBuildValidator_MockMode(t *testing.T) {
	t.Parallel()

	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "mock"})
	require.NoError(t, err, "BuildValidator(mock)")
	t.Cleanup(cleanup)

	require.NotNil(t, v, "mock validator must not be nil")
}

// Not parallel: Register writes the package-level mode registry, which the
// parallel tests read unsynchronized; sequential tests finish before they resume.
func TestBuildValidator_ReturnsNoopCleanup_WhenModeHasNone(t *testing.T) {
	// A mode without resources to release (like xsuaa) returns a nil cleanup.
	auth.Register("test-nil-cleanup", func(auth.ModeConfig) (auth.Validator, func(), error) {
		return nil, nil, nil
	})

	_, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "test-nil-cleanup"})

	require.NoError(t, err)
	require.NotNil(t, cleanup, "callers defer cleanup unconditionally")
	cleanup()
}

func TestBuildValidator_RejectsInvalidMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		mode             string
		wantErrIs        error
		wantErrSubstring string
	}{
		{
			// In default builds (no -tags devauth) "off" must be unavailable.
			name:      "off_default_build",
			mode:      "off",
			wantErrIs: auth.ErrModeUnavailable,
		},
		{
			name:             "unknown_mode_name",
			mode:             "weird",
			wantErrSubstring: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: tc.mode})

			require.Error(t, err, "BuildValidator(%q) must reject", tc.mode)
			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			}
			if tc.wantErrSubstring != "" {
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			}
		})
	}
}

func TestBuildValidator_MockMode_CleanupClosesTheKeySource(t *testing.T) {
	t.Parallel()

	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "mock"})
	require.NoError(t, err, "BuildValidator(mock)")

	cleanup()
	_, err = v.Validate(context.Background(), "not.a.jwt")

	require.ErrorIs(t, err, auth.ErrKeySourceClosed, "cleanup must close the validator, not only the mock server")
}
