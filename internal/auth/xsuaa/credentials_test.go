//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

const fixtureVCAP = `{
  "xsuaa": [{
    "credentials": {
      "clientid":         "sb-kafkito!t12345",
      "clientsecret":     "secret",
      "credential-type":  "binding-secret",
      "url":              "https://example-tenant.authentication.eu10.hana.ondemand.com",
      "uaadomain":        "authentication.eu10.hana.ondemand.com",
      "xsappname":        "kafkito!t12345",
      "identityzoneid":   "abc-123",
      "tenantmode":       "dedicated"
    },
    "label": "xsuaa", "name": "kafkito-uaa", "plan": "application", "tags": ["xsuaa"]
  }]
}`

func TestParseCredentialsFromVCAP_ExtractsAllFields(t *testing.T) {
	t.Parallel()

	c, err := xsuaa.ParseCredentialsFromVCAP(fixtureVCAP)
	require.NoError(t, err, "ParseCredentialsFromVCAP must accept the canonical fixture")

	cases := []struct {
		field string
		got   string
		want  string
	}{
		{"ClientID", c.ClientID, "sb-kafkito!t12345"},
		{"UAADomain", c.UAADomain, "authentication.eu10.hana.ondemand.com"},
		{"XSAppName", c.XSAppName, "kafkito!t12345"},
		{"LocalScopePrefix", c.LocalScopePrefix(), "kafkito!t12345."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.got)
		})
	}
}

func TestParseCredentialsFromVCAP_RejectsEmptyXSUAAArray(t *testing.T) {
	t.Parallel()

	_, err := xsuaa.ParseCredentialsFromVCAP(`{"xsuaa": []}`)

	require.Error(t, err, "empty xsuaa binding array must be rejected")
}
