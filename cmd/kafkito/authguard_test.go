// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuardAuthMode(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cases := []struct {
		name    string
		mode    string
		addr    string
		env     map[string]string
		wantErr bool
	}{
		{name: "non_off_mode_always_ok", mode: "oidc", addr: ":8080", wantErr: false},
		{name: "off_on_loopback_ok", mode: "off", addr: "127.0.0.1:8080", wantErr: false},
		{name: "off_on_localhost_ok", mode: "off", addr: "localhost:8080", wantErr: false},
		{name: "off_on_cloudfoundry_blocked", mode: "off", addr: "127.0.0.1:8080",
			env: map[string]string{"VCAP_APPLICATION": "{}"}, wantErr: true},
		{name: "off_on_public_bind_blocked", mode: "off", addr: ":8080", wantErr: true},
		{name: "off_on_public_bind_acknowledged_ok", mode: "off", addr: ":8080",
			env: map[string]string{"KAFKITO_INSECURE_AUTH_OFF": "true"}, wantErr: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guardAuthMode(tc.mode, tc.addr, env(tc.env))
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
