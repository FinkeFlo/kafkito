//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

// NewValidatorForTest is NewValidator plus one test-only relaxation: a jku
// may use plain http when its host is a loopback IP, so tests can point it
// at an httptest server. Production code cannot reach it.
func NewValidatorForTest(creds Credentials) (*Validator, error) {
	return newValidator(creds, true)
}
