// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

//go:build !devauth

package auth

// init registers the "off" mode for builds without the devauth tag, where it
// is unavailable. devauth builds register their own "off" in mode_devauth.go
// instead, so neither registration depends on init order.
func init() {
	Register("off", newOffMode)
}

// newOffMode is the "off" factory for builds without the devauth tag: running
// without authentication is refused with ErrModeUnavailable.
func newOffMode(_ ModeConfig) (Validator, func(), error) {
	return nil, nil, ErrModeUnavailable
}
