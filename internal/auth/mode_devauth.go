// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

//go:build devauth

package auth

import "context"

// init registers "off" with the synthetic-principal validator. mode_off.go,
// which registers the unavailable "off", is excluded from devauth builds, so
// this is the only "off" registration and does not depend on init order.
func init() {
	Register("off", newOffModeDevauth)
}

// alwaysAllowValidator stamps the same synthetic Principal on every call.
// Compiled only into the devauth build (off-mode bypass for local dev).
type alwaysAllowValidator struct {
	principal *Principal
}

func (a alwaysAllowValidator) Validate(_ context.Context, _ string) (*Principal, error) {
	return a.principal, nil
}

func (a alwaysAllowValidator) SyntheticPrincipal() *Principal {
	return a.principal
}

func newOffModeDevauth(_ ModeConfig) (Validator, func(), error) {
	return alwaysAllowValidator{
		principal: &Principal{
			Subject: "dev-user",
			Email:   "dev@local",
			Scopes:  []string{"Display"},
		},
	}, func() {}, nil
}
