// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"fmt"
	"sync"
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

func TestBuildValidator_ReturnsNoopCleanup_WhenModeHasNone(t *testing.T) {
	t.Parallel()

	// A mode without resources to release (like xsuaa) returns a nil cleanup.
	auth.Register("test-nil-cleanup", func(auth.ModeConfig) (auth.Validator, func(), error) {
		return nil, nil, nil
	})

	_, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "test-nil-cleanup"})

	require.NoError(t, err)
	require.NotNil(t, cleanup, "callers defer cleanup unconditionally")
	cleanup()
}

func TestBuildValidator_RejectsUnknownMode(t *testing.T) {
	t.Parallel()

	_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "weird"})

	require.Error(t, err, "BuildValidator must reject an unregistered mode")
	assert.ErrorContains(t, err, "unknown")
}

func TestBuildValidator_MockMode_CleanupClosesTheKeySource(t *testing.T) {
	t.Parallel()

	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "mock"})
	require.NoError(t, err, "BuildValidator(mock)")

	cleanup()
	_, err = v.Validate(context.Background(), "not.a.jwt")

	require.ErrorIs(t, err, auth.ErrKeySourceClosed, "cleanup must close the validator, not only the mock server")
}

// Register and BuildValidator share the mode registry; run with -race.
func TestRegistry_IsSafeForConcurrentRegisterAndBuild(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	for i := range 8 {
		name := fmt.Sprintf("test-concurrent-%d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			auth.Register(name, func(auth.ModeConfig) (auth.Validator, func(), error) {
				return nil, nil, nil
			})
		}()
		go func() {
			defer wg.Done()
			_, _, _ = auth.BuildValidator(auth.ModeConfig{Mode: name})
		}()
	}
	wg.Wait()

	for i := range 8 {
		_, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: fmt.Sprintf("test-concurrent-%d", i)})
		require.NoError(t, err)
		cleanup()
	}
}
