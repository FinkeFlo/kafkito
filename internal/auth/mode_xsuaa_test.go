//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"

	// Side-effect import: xsuaa.init() registers itself with the auth-mode
	// registry. Without this import the "xsuaa" mode would be unknown even on
	// btp builds (no file in package auth is allowed to import the xsuaa
	// subpackage — that would form an import cycle).
	_ "github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

// XSUAA mode is only registered when built with `-tags btp`. Without
// VCAP_SERVICES, BuildValidator must surface a VCAP-shaped error rather than
// the generic "unknown mode" error returned by the default registry.
func TestBuildValidator_XSUAAMode_RequiresVCAP(t *testing.T) {
	t.Parallel()

	_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "xsuaa", VCAPServices: ""})

	require.Error(t, err, "BuildValidator(xsuaa) without VCAP must reject")
	assert.ErrorContains(t, err, "VCAP")
}
