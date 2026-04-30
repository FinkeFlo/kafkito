//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"os"

	"github.com/FinkeFlo/kafkito/internal/auth"

	// Side-effect import: xsuaa.init() registers itself with the auth-mode
	// registry so cfg.Mode == "xsuaa" resolves to a real validator. Package
	// internal/auth cannot perform this registration itself because xsuaa
	// imports auth for the Validator/Principal types — having auth import xsuaa
	// would form a cycle. Pulling the registration in at the binary entrypoint
	// keeps the auth core OSS-clean while the btp build still wires xsuaa up.
	_ "github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

// populateAuthConfigFromEnv is the btp-build hook: read the VCAP_SERVICES JSON
// blob (Cloud Foundry XSUAA service binding) and tag the request with a local
// XSAppName so non-xsuaa modes (mock) running in a btp-tagged binary still
// receive a sensible audience label. xsuaa mode reads xsappname from the
// VCAP_SERVICES payload, so the literal here is ignored when Mode == "xsuaa".
func populateAuthConfigFromEnv(c *auth.ModeConfig) {
	c.VCAPServices = os.Getenv("VCAP_SERVICES")
	c.XSAppName = "kafkito!t-local"
}
