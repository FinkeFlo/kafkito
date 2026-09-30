//go:build !devauth

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// Without the devauth tag (default and btp builds) "off" is registered but
// refuses to build, rather than being unknown.
func TestBuildValidator_OffMode_IsUnavailableWithoutDevauth(t *testing.T) {
	t.Parallel()

	_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "off"})

	require.ErrorIs(t, err, auth.ErrModeUnavailable)
}
