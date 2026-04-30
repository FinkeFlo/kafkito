// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"errors"
	"fmt"
)

// ErrModeUnavailable indicates the requested KAFKITO_AUTH_MODE is not compiled in.
// Most notably, "off" is gated behind the `devauth` build tag.
var ErrModeUnavailable = errors.New("auth mode unavailable in this build")

// ModeConfig drives validator construction. Fields not relevant to a given
// mode are simply ignored by that mode's factory.
type ModeConfig struct {
	// Mode selects which registered factory to use (e.g. "off", "mock"). The
	// set of valid values depends on the build tags the binary was compiled
	// with: a default build registers "off" and "mock"; tagged builds may
	// register additional IdP-specific modes.
	Mode string
	// VCAPServices is the raw VCAP_SERVICES JSON, used by IdP modes that
	// expect their credentials in a Cloud Foundry service binding.
	VCAPServices string
	// XSAppName is an IdP-specific application identifier consumed by modes
	// that need it. Ignored by the generic modes.
	XSAppName string
}

// ModeFactory constructs a Validator for a registered auth mode. The returned
// cleanup function (may be nil) is run on shutdown by callers — used by the
// mock mode to stop its embedded JWKS server.
type ModeFactory func(cfg ModeConfig) (Validator, func(), error)

// modes is the registry of mode-name -> factory. Generic modes register
// themselves from init() in mode_default.go; IdP-specific subpackages register
// themselves via init() behind their respective build tags. Look up via
// BuildValidator.
var modes = map[string]ModeFactory{}

// Register binds a ModeFactory to the given mode name. Intended for use from
// init() in mode-specific files. Re-registering a name overwrites the previous
// factory; this is how mode_devauth.go upgrades "off" from the default
// (unavailable) to the synthetic-principal variant.
func Register(name string, factory ModeFactory) {
	modes[name] = factory
}

// BuildValidator returns the configured validator plus an optional cleanup
// function callers should defer (used by mock mode to stop the embedded server).
func BuildValidator(cfg ModeConfig) (Validator, func(), error) {
	f, ok := modes[cfg.Mode]
	if !ok {
		return nil, nil, fmt.Errorf("unknown KAFKITO_AUTH_MODE %q (build with appropriate tags?)", cfg.Mode)
	}
	return f(cfg)
}
