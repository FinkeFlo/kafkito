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

// populateAuthConfigFromEnv is the btp-build hook: it passes the VCAP_SERVICES
// JSON blob (Cloud Foundry XSUAA service binding) to the auth modes. xsuaa mode
// reads its credentials, including xsappname, from that payload.
func populateAuthConfigFromEnv(c *auth.ModeConfig) {
	// VCAP_SERVICES is set by the platform, not a kafkito setting, so it is
	// read from the environment instead of through config.Load.
	c.VCAPServices = os.Getenv("VCAP_SERVICES")
}
