//go:build devauth

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestBuildValidator_OffMode_Devauth(t *testing.T) {
	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "off"})
	if err != nil {
		t.Fatalf("off mode under devauth: %v", err)
	}
	defer cleanup()
	if v == nil {
		t.Fatalf("nil validator")
	}
	// In off mode the validator must accept any (or empty) token and stamp a synthetic principal.
	p, err := v.Validate(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("Validate under off: %v", err)
	}
	if p == nil || p.Subject == "" {
		t.Errorf("synthetic principal missing or empty: %+v", p)
	}
}
