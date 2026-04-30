//go:build !devauth

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestBuildValidator_MockMode(t *testing.T) {
	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "mock", XSAppName: "kafkito!t1"})
	if err != nil {
		t.Fatalf("mock: %v", err)
	}
	defer cleanup()
	if v == nil {
		t.Fatalf("validator nil")
	}
}

// In default builds (no -tags devauth) "off" must be unavailable.
func TestBuildValidator_OffMode_Default(t *testing.T) {
	_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "off"})
	if err == nil || !errors.Is(err, auth.ErrModeUnavailable) {
		t.Errorf("want ErrModeUnavailable, got %v", err)
	}
}

func TestBuildValidator_UnknownMode(t *testing.T) {
	_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "weird"})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("want unknown-mode error, got %v", err)
	}
}
