//go:build !btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import "github.com/FinkeFlo/kafkito/internal/auth"

// populateAuthConfigFromEnv is the default-build hook: no environment-derived
// fields are needed for the generic auth modes ("off", "mock", "oidc"), so
// this is a deliberate no-op. Tagged builds (e.g. -tags btp) override this
// function in their own _<tag>.go file to wire IdP-specific bindings.
func populateAuthConfigFromEnv(*auth.ModeConfig) {}
