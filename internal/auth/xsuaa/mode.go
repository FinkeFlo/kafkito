//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"errors"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// init registers the "xsuaa" mode with the auth registry. The registration
// lives in this package (not in package auth) because package auth must not
// import the xsuaa subpackage — doing so would create an import cycle, since
// xsuaa imports auth for the Validator interface, Principal, ModeConfig, and
// the generic JWT helpers. Callers that need the xsuaa mode (the kafkito
// binary on btp builds, btp-tagged tests) blank-import this package to
// trigger the side-effect registration.
func init() {
	auth.Register("xsuaa", NewMode)
}

// NewMode is the auth.ModeFactory for the "xsuaa" mode. It parses the
// VCAP_SERVICES JSON from cfg, builds an XSUAA Validator, and returns it.
func NewMode(cfg auth.ModeConfig) (auth.Validator, func(), error) {
	if cfg.VCAPServices == "" {
		return nil, nil, errors.New("xsuaa mode requires VCAP_SERVICES")
	}
	creds, err := ParseCredentialsFromVCAP(cfg.VCAPServices)
	if err != nil {
		return nil, nil, err
	}
	v, err := NewValidator(creds)
	if err != nil {
		return nil, nil, err
	}
	return v, nil, nil
}
