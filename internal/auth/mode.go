// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"errors"
	"fmt"
	"sync"
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
}

// ModeFactory constructs a Validator for a registered auth mode. The returned
// cleanup function may be nil when the mode has nothing to release;
// BuildValidator replaces it with a no-op so callers can always defer it.
type ModeFactory func(cfg ModeConfig) (Validator, func(), error)

// modes is the registry of mode-name -> factory. Generic modes register
// themselves from init() in mode_default.go, mode_off.go and mode_devauth.go;
// IdP-specific subpackages register themselves via init() behind their
// respective build tags. Look up via BuildValidator.
var modes = map[string]ModeFactory{}

// modesMu guards modes, so Register and BuildValidator are safe to call
// concurrently (tests register modes while others build validators).
var modesMu sync.RWMutex

// Register binds a ModeFactory to the given mode name. Intended for use from
// init() in mode-specific files; each build registers every name once, so the
// result does not depend on init order. Re-registering a name overwrites the
// previous factory.
func Register(name string, factory ModeFactory) {
	modesMu.Lock()
	defer modesMu.Unlock()
	modes[name] = factory
}

// BuildValidator returns the configured validator plus a cleanup function
// callers should defer (mock mode uses it to stop the embedded server). On
// success the cleanup is never nil.
func BuildValidator(cfg ModeConfig) (Validator, func(), error) {
	// The lock covers only the lookup; the factory runs unlocked because it
	// may block (mock mode starts a server and loads keys).
	modesMu.RLock()
	f, ok := modes[cfg.Mode]
	modesMu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("unknown KAFKITO_AUTH_MODE %q (build with appropriate tags?)", cfg.Mode)
	}
	v, cleanup, err := f(cfg)
	if err != nil {
		return nil, nil, err
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	return v, cleanup, nil
}
