// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// run mutates the default slog logger; restore it for the rest of the package.
func restoreDefaultLogger(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestRun_ReturnsExitCode2_WhenConfigFileIsMissing(t *testing.T) {
	restoreDefaultLogger(t)

	assert.Equal(t, 2, run(filepath.Join(t.TempDir(), "missing.yaml")))
}

// Mode "off" is refused either at auth init (builds without it) or by the
// loopback guard (non-loopback bind, no acknowledgement); both exit with 2.
func TestRun_ReturnsExitCode2_WhenAuthModeOffIsRefused(t *testing.T) {
	restoreDefaultLogger(t)
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_AUTH_MODE", "off")
	t.Setenv("KAFKITO_INSECURE_AUTH_OFF", "")
	t.Setenv("VCAP_APPLICATION", "")
	t.Setenv("PORT", "0")

	assert.Equal(t, 2, run(""))
}
